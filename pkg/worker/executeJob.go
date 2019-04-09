package worker

import (
	"context"
	"docker.io/go-docker/api/types"
	"docker.io/go-docker/api/types/container"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/docker/go-connections/nat"
	"github.com/mholt/archiver"
	"github.com/shirou/gopsutil/disk"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrysclient/pkg/poll"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	pidsLimit = 200
)

var maxTimeout = 60 * 10 // job server has a 10 minute timeout

func (w *Worker) executeJob(ctx context.Context, u url.URL, jID string) {
	w.JobID = jID
	w.Busy = true
	defer func() {
		w.Busy = false
		w.JobID = ""
		w.sshKey = []byte{}
		w.notebook = false
	}()
	*w.JobsInProcess++
	defer func() { *w.JobsInProcess-- }()
	dStr := strconv.Itoa(int(w.Device))
	if err := check.ContextCanceled(ctx); err != nil {
		log.Printf("Device %s: miner canceled job execution: %v", dStr, err)
		return
	}
	w.Miner.Stop()
	defer w.Miner.Start()

	jobFinished := make(chan struct{})
	defer func() {
		close(jobFinished)
	}()
	jobCanceled := make(chan struct{})
	go func(u url.URL) {
		// poll to check if job is canceled
		p := path.Join("job", w.JobID, "cancel")
		u.Path = p
		q := u.Query()
		q.Set("timeout", fmt.Sprintf("%d", maxTimeout))
		buffer := int64(10)
		sinceTime := (time.Now().Unix() - buffer) * 1000
		q.Set("since_time", fmt.Sprintf("%d", sinceTime))
		u.RawQuery = q.Encode()
		for {
			if err := check.ContextCanceled(ctx); err != nil {
				log.Printf("Device %s: miner canceled job execution: %v", dStr, err)
				return
			}
			select {
			case <-jobFinished:
				return
			default:
			}
			pr := poll.Response{}
			operation := func() error {
				req, err := http.NewRequest(http.MethodGet, u.String(), nil)
				if err != nil {
					return err
				}
				req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", *w.AuthToken))
				req = req.WithContext(ctx)

				resp, err := w.Client.Do(req)
				if err != nil {
					return err
				}
				defer check.Err(resp.Body.Close)

				if resp.StatusCode == http.StatusBadGateway {
					return fmt.Errorf("server: temporary error")
				} else if resp.StatusCode >= 300 {
					b, _ := ioutil.ReadAll(resp.Body)
					return fmt.Errorf("server: %v", string(b))
				}

				if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
					return fmt.Errorf("decoding response: %v", err)
				}

				return nil
			}
			if err := backoff.RetryNotify(operation,
				backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries), ctx),
				func(err error, t time.Duration) {
					log.Printf("Device %s: error polling job canceled: %v", dStr, err)
					log.Printf("Device %s: retrying in %s seconds\n", dStr, t.Round(time.Second).String())
				}); err != nil {
				log.Printf("Device %s: error polling job canceled: %v", dStr, err)
			}

			if len(pr.Events) > 0 {
				close(jobCanceled)
				return
			}

			if pr.Timestamp > sinceTime {
				sinceTime = pr.Timestamp
			}

			q = u.Query()
			q.Set("since_time", fmt.Sprintf("%d", sinceTime))
			u.RawQuery = q.Encode()
		}
	}(u)

	currUser, err := user.Current()
	if err != nil {
		log.Printf("Device %s: error getting current user: %v", dStr, err)
		return
	}
	if os.Geteuid() == 0 {
		currUser, err = user.Lookup(os.Getenv("SUDO_USER"))
		if err != nil {
			log.Printf("Device %s: error getting current sudo user: %v", dStr, err)
			return
		}
	}
	jobDir := filepath.Join(currUser.HomeDir, ".emrys", w.JobID)
	if err = os.MkdirAll(jobDir, 0755); err != nil {
		log.Printf("Device %s: error making job dir %v: %v", dStr, jobDir, err)
		return
	}
	defer check.Err(func() error { return os.RemoveAll(jobDir) })

	sshKeyFile, err := w.saveSSHKey()
	if err != nil {
		log.Printf("Device %s: error saving ssh-key: %v", dStr, err)
		return
	}
	defer func() {
		if err := os.Remove(sshKeyFile); err != nil {
			log.Printf("Notebook: error removing ssh key: %v", err)
			return
		}
	}()

	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	registry := "registry.emrys.io"
	repo := "miner"
	imgRefStr := fmt.Sprintf("%s/%s/%s:latest", registry, repo, w.JobID)
	go w.downloadImage(ctx, &wg, errCh, u, imgRefStr)
	defer func() {
		ctx := context.Background()
		log.Printf("Device %s: Removing image...\n", dStr)
		if _, err := w.Docker.ImageRemove(ctx, imgRefStr, types.ImageRemoveOptions{
			Force: true,
		}); err != nil {
			log.Printf("Device %s: error removing job image %v: %v", dStr, w.JobID, err)
		}
		if _, err := w.Docker.BuildCachePrune(ctx); err != nil {
			log.Printf("Device %s: error pruning build cache: %v", dStr, err)
		}
	}()

	go w.downloadData(ctx, &wg, errCh, u, jobDir)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-errCh:
		return
	case <-jobCanceled:
		log.Printf("Device %s: job canceled by user\n", dStr)
		return
	}
	if err := check.ContextCanceled(ctx); err != nil {
		log.Printf("Device %s: miner canceled job execution: %v", dStr, err)
		return
	}

	fileInfos, err := ioutil.ReadDir(jobDir)
	if err != nil {
		log.Printf("Device %s: error reading job dir %s: %v", dStr, jobDir, err)
		return
	}
	var hostDataDir string
	if len(fileInfos) > 0 {
		hostDataDir = filepath.Join(jobDir, fileInfos[0].Name())
	} else {
		hostDataDir = filepath.Join(jobDir, "data")
		if _, err := os.Create(hostDataDir); err != nil {
			log.Printf("Device %s: error creating empty data dir %s: %v", dStr, hostDataDir, err)
			return
		}
	}
	w.DataDir = hostDataDir
	defer func() { w.DataDir = "" }()

	dataDirUsage, err := disk.Usage(w.DataDir)
	if err != nil {
		log.Printf("Device %s: error getting disk usage: data folder: %v", dStr, err)
		return
	}
	sizeDataDir := dataDirUsage.Total

	hostOutputDir := filepath.Join(jobDir, "output")
	w.OutputDir = hostOutputDir
	defer func() { w.OutputDir = "" }()

	oldUMask := syscall.Umask(000)
	if err = os.Chmod(hostDataDir, 0777); err != nil {
		log.Printf("Device %s: error modifying data dir %v permissions: %v", dStr, hostDataDir, err)
		_ = syscall.Umask(oldUMask)
		return
	}
	if err = os.MkdirAll(hostOutputDir, 0777); err != nil {
		log.Printf("Device %s: error making output dir %v: %v", dStr, hostOutputDir, err)
		_ = syscall.Umask(oldUMask)
		return
	}
	_ = syscall.Umask(oldUMask)

	userHome := "/home/user"
	dockerDataDir := filepath.Join(userHome, filepath.Base(hostDataDir))
	dockerOutputDir := filepath.Join(userHome, "output")
	if err = os.Setenv("NVIDIA_VISIBLE_DEVICES", dStr); err != nil {
		log.Printf("Device %s: error setting NVIDIA_VISIBLE_DEVICES=%s: %v", dStr, dStr, err)
		return
	}
	// TODO: unsetting env at end of job could potentially interfere with another gpu's job I think?
	defer check.Err(func() error { return os.Unsetenv("NVIDIA_VISIBLE_DEVICES") })

	var exposedPorts nat.PortSet
	var portBindings nat.PortMap
	if w.notebook {
		exposedPorts = nat.PortSet{
			"8888/tcp": struct{}{},
		}
		portBindings = nat.PortMap{
			"8888/tcp": []nat.PortBinding{
				nat.PortBinding{
					HostIP:   "0.0.0.0",
					HostPort: w.Port,
				},
			},
		}
	}
	c, err := w.Docker.ContainerCreate(ctx, &container.Config{
		ExposedPorts: exposedPorts,
		Image:        imgRefStr,
		Tty:          true,
	}, &container.HostConfig{
		Binds: []string{
			fmt.Sprintf("%s:%s:rw", hostDataDir, dockerDataDir),
			fmt.Sprintf("%s:%s:rw", hostOutputDir, dockerOutputDir),
		},
		CapDrop: []string{
			"ALL",
		},
		PortBindings: portBindings,
		// ReadonlyRootfs: true, // TODO
		Runtime: "nvidia",
		Resources: container.Resources{
			DiskQuota:         int64(w.Disk - sizeDataDir),
			MemoryReservation: int64(w.RAM),
			MemorySwap:        int64(w.RAM),
			PidsLimit:         pidsLimit,
		},
		SecurityOpt: []string{
			"no-new-privileges",
		},
	}, nil, "")
	if err != nil {
		log.Printf("Device %s: error creating container: %v", dStr, err)
		return
	}
	w.ContainerID = c.ID
	defer func() { w.ContainerID = "" }()

	if err := check.ContextCanceled(ctx); err != nil {
		log.Printf("Device %s: miner canceled job execution: %v", dStr, err)
		return
	}

	log.Printf("Device %s: Running container...\n", dStr)
	if err := w.Docker.ContainerStart(ctx, c.ID, types.ContainerStartOptions{}); err != nil {
		log.Printf("Device %s: error starting container: %v", dStr, err)
		return
	}
	defer func() {
		ctx := context.Background()
		log.Printf("Device %s: Removing container...\n", dStr)
		if err := w.Docker.ContainerRemove(ctx, c.ID, types.ContainerRemoveOptions{
			Force: true,
		}); err != nil {
			log.Printf("Device %s: error removing job container %v: %v", dStr, w.JobID, err)
		}
	}()

	out, err := w.Docker.ContainerLogs(ctx, c.ID, types.ContainerLogsOptions{
		Follow:     true,
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		log.Printf("Device %s: error logging container: %v", dStr, err)
		return
	}
	defer check.Err(out.Close)

	if w.notebook {
		log.Printf("Device %s: Forwarding port...\n", dStr)
		sshCmd := w.sshRemoteForward(ctx, sshKeyFile)
		if err = sshCmd.Start(); err != nil {
			log.Printf("Device %s: error remote forwarding notebook requests: %v", dStr, err)
			return
		}
		defer func() {
			if err := sshCmd.Process.Kill(); err != nil {
				log.Printf("Device %s: error killing remote forwarding process: %v", dStr, err)
				return
			}
		}()
	}

	log.Printf("Device %s: Uploading log...\n", dStr)
	logStrCh := make(chan string)
	logErrCh := make(chan error)
	go func() {
		body := make([]byte, 4096)
		for {
			n, err := out.Read(body)
			if err != nil {
				logErrCh <- err
				return
			}
			logStrCh <- string(body[:n])
		}
	}()

	var operation func() error
	jCanceled := false
	p := path.Join("job", w.JobID, "log")
	u.Path = p
loop:
	for {
		select {
		case <-jobCanceled:
			log.Printf("Device %s: job canceled by user...\n", dStr)
			jCanceled = true
			operation := func() error {
				var body *strings.Reader
				if w.DiskQuotaExceeded {
					defer func() { w.DiskQuotaExceeded = false }()
					body = strings.NewReader("JOB CANCELED: USER EXCEEDED DISK QUOTA\n")
				} else {
					body = strings.NewReader("JOB CANCELED BY USER.\n")
				}

				req, err := http.NewRequest(http.MethodPost, u.String(), body)
				if err != nil {
					return err
				}
				req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", *w.AuthToken))
				req = req.WithContext(ctx)

				resp, err := w.Client.Do(req)
				if err != nil {
					return err
				}
				defer check.Err(resp.Body.Close)

				if resp.StatusCode == http.StatusBadGateway {
					return fmt.Errorf("server: temporary error")
				} else if resp.StatusCode >= 300 {
					b, _ := ioutil.ReadAll(resp.Body)
					return fmt.Errorf("server: %v", string(b))
				}

				return nil
			}
			if err := backoff.RetryNotify(operation,
				backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries), ctx),
				func(err error, t time.Duration) {
					log.Printf("Device %s: error uploading output: %v", dStr, err)
					log.Printf("Device %s: retrying in %s seconds\n", dStr, t.Round(time.Second).String())
				}); err != nil {
				log.Printf("Device %s: error uploading output: %v", dStr, err)
				return
			}

			goto FinishLogAndUploadData
		case err := <-logErrCh:
			if err != io.EOF {
				log.Printf("Device %s: error reading container logs: %v", dStr, err)
				return
			}
			break loop
		case logStr := <-logStrCh:
			if err := check.ContextCanceled(ctx); err != nil {
				log.Printf("Device %s: miner canceled job execution: %v", dStr, err)
				return
			}
			operation := func() error {
				req, err := http.NewRequest(http.MethodPost, u.String(), strings.NewReader(logStr))
				if err != nil {
					return err
				}
				req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", *w.AuthToken))
				req = req.WithContext(ctx)

				resp, err := w.Client.Do(req)
				if err != nil {
					return err
				}
				defer check.Err(resp.Body.Close)

				if resp.StatusCode == http.StatusBadGateway {
					return fmt.Errorf("server: temporary error")
				} else if resp.StatusCode >= 300 {
					b, _ := ioutil.ReadAll(resp.Body)
					return fmt.Errorf("server: %v", string(b))
				}

				return nil
			}
			if err := backoff.RetryNotify(operation,
				backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries), ctx),
				func(err error, t time.Duration) {
					log.Printf("Device %s: error uploading output: %v", dStr, err)
					log.Printf("Device %s: retrying in %s seconds\n", dStr, t.Round(time.Second).String())
				}); err != nil {
				log.Printf("Device %s: error uploading output: %v", dStr, err)
				return
			}
		}
	}

FinishLogAndUploadData:
	operation = func() error {
		// POST with empty body signifies log upload complete
		req, err := http.NewRequest(http.MethodPost, u.String(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", *w.AuthToken))

		resp, err := w.Client.Do(req)
		if err != nil {
			return err
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode == http.StatusBadGateway {
			return fmt.Errorf("server: temporary error")
		} else if resp.StatusCode >= 300 {
			b, _ := ioutil.ReadAll(resp.Body)
			return fmt.Errorf("server: %v", string(b))
		}

		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Device %s: error uploading output: %v", dStr, err)
			log.Printf("Device %s: retrying in %s seconds\n", dStr, t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Device %s: error uploading output: %v", dStr, err)
		return
	}
	log.Printf("Device %s: Log uploaded!\n", dStr)

	if err := check.ContextCanceled(ctx); err != nil {
		log.Printf("Device %s: miner canceled job execution: %v", dStr, err)
		return
	}
	log.Printf("Device %s: Uploading data...\n", dStr)
	p = path.Join("job", w.JobID, "data")
	u.Path = p
	if jCanceled {
		q := u.Query()
		q.Set("jobcanceled", "1")
		u.RawQuery = q.Encode()
	}
	operation = func() error {
		pr, pw := io.Pipe()
		go func() {
			defer check.Err(pw.Close)
			files, err := ioutil.ReadDir(hostOutputDir)
			outputFiles := make([]string, 0, len(files))
			if err != nil {
				log.Printf("Device %s: error uploading output: reading files in output directory %v: %v\n", dStr, hostOutputDir, err)
				return
			}
			for _, file := range files {
				outputFile := filepath.Join(hostOutputDir, file.Name())
				outputFiles = append(outputFiles, outputFile)
			}
			if err = archiver.TarGz.Write(pw, outputFiles); err != nil {
				log.Printf("Device %s: error uploading output: packing output directory %v: %v\n", dStr, hostOutputDir, err)
				return
			}
		}()

		req, err := http.NewRequest(http.MethodPost, u.String(), pr)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", *w.AuthToken))
		req = req.WithContext(ctx)

		resp, err := w.Client.Do(req)
		if err != nil {
			return err
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode == http.StatusBadGateway {
			return fmt.Errorf("server: temporary error")
		} else if resp.StatusCode >= 300 {
			b, _ := ioutil.ReadAll(resp.Body)
			return fmt.Errorf("server: %v", string(b))
		}

		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Device %s: error uploading output: %v", dStr, err)
			log.Printf("Device %s: retrying in %s seconds\n", dStr, t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Device %s: error uploading output: %v", dStr, err)
		return
	}

	log.Printf("Device %s: Job completed!\n", dStr)
}
