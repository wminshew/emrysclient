package mine

import (
	"bytes"
	"context"
	"docker.io/go-docker"
	"docker.io/go-docker/api/types"
	"docker.io/go-docker/api/types/container"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/mholt/archiver"
	"github.com/wminshew/emrys/pkg/check"
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
	"sync"
	"syscall"
	"time"
)

func (w *worker) executeJob(ctx context.Context, dClient *docker.Client, client *http.Client, u url.URL, mID, jID, authToken, dockerAuthStr string) {
	w.busy = true
	w.jID = jID
	defer func() {
		w.busy = false
		w.jID = ""
	}()
	jobsInProcess++
	defer func() { jobsInProcess-- }()
	dStr := strconv.Itoa(int(w.device))
	if err := checkContextCanceled(ctx); err != nil {
		log.Printf("Device %s: miner canceled job execution: %v", dStr, err)
		return
	}
	w.miner.stop()
	defer w.miner.start()

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
	jobDir := filepath.Join(currUser.HomeDir, ".emrys", jID)
	if err = os.MkdirAll(jobDir, 0755); err != nil {
		log.Printf("Device %s: error making job dir %v: %v", dStr, jobDir, err)
		return
	}
	defer check.Err(func() error { return os.RemoveAll(jobDir) })

	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	registry := "registry.emrys.io"
	repo := "miner"
	imgRefStr := fmt.Sprintf("%s/%s/%s:latest", registry, repo, jID)
	go downloadImage(ctx, &wg, errCh, client, u, dClient, imgRefStr, jID, authToken, dockerAuthStr)
	defer func() {
		if _, err := dClient.ImageRemove(ctx, imgRefStr, types.ImageRemoveOptions{
			Force: true,
		}); err != nil {
			log.Printf("Device %s: error removing job image %v: %v", dStr, jID, err)
		}
		if _, err := dClient.BuildCachePrune(ctx); err != nil {
			log.Printf("Device %s: error pruning build cache: %v", dStr, err)
		}
	}()

	go downloadData(ctx, &wg, errCh, client, u, jID, authToken, jobDir)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-errCh:
		return
	}
	if err := checkContextCanceled(ctx); err != nil {
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

	hostOutputDir := filepath.Join(jobDir, "output")
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
	defer check.Err(func() error { return os.Unsetenv("NVIDIA_VISIBLE_DEVICES") })
	c, err := dClient.ContainerCreate(ctx, &container.Config{
		Image: imgRefStr,
		Tty:   true,
	}, &container.HostConfig{
		AutoRemove: true,
		Binds: []string{
			fmt.Sprintf("%s:%s:rw", hostDataDir, dockerDataDir),
			fmt.Sprintf("%s:%s:rw", hostOutputDir, dockerOutputDir),
		},
		CapDrop: []string{
			"ALL",
		},
		// ReadonlyRootfs: true, // TODO
		Runtime: "nvidia",
		SecurityOpt: []string{
			"no-new-privileges",
		},
	}, nil, "")
	if err != nil {
		log.Printf("Device %s: error creating container: %v", dStr, err)
		return
	}
	if err := checkContextCanceled(ctx); err != nil {
		log.Printf("Device %s: miner canceled job execution: %v", dStr, err)
		return
	}

	log.Printf("Device %s: Running container...\n", dStr)
	if err := dClient.ContainerStart(ctx, c.ID, types.ContainerStartOptions{}); err != nil {
		log.Printf("Device %s: error starting container: %v", dStr, err)
		return
	}

	out, err := dClient.ContainerLogs(ctx, c.ID, types.ContainerLogsOptions{
		Follow:     true,
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		log.Printf("Device %s: error logging container: %v", dStr, err)
		return
	}
	defer check.Err(out.Close)

	maxUploadRetries := uint64(10)
	body := make([]byte, 4096)
	m := "POST"
	p := path.Join("job", jID, "log")
	u.Path = p
	var n int
	for n, err = out.Read(body); err == nil; n, err = out.Read(body) {
		if err := checkContextCanceled(ctx); err != nil {
			log.Printf("Device %s: miner canceled job execution: %v", dStr, err)
			return
		}
		operation := func() error {
			req, err := http.NewRequest(m, u.String(), bytes.NewReader(body[:n]))
			if err != nil {
				return fmt.Errorf("creating request %v %v: %v", m, u.Path, err)
			}
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
			req = req.WithContext(ctx)

			resp, err := client.Do(req)
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
			backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxUploadRetries), ctx),
			func(err error, t time.Duration) {
				log.Printf("Device %s: error uploading output: %v", dStr, err)
				log.Printf("Device %s: retrying in %s seconds\n", dStr, t.Round(time.Second).String())
			}); err != nil {
			log.Printf("Device %s: error uploading output: %v", dStr, err)
			return
		}
	}
	if err != nil && err != io.EOF {
		log.Printf("Device %s: error reading log buffer: %v", dStr, err)
		return
	}

	operation := func() error {
		// POST with empty body signifies log upload complete
		req, err := http.NewRequest(m, u.String(), nil)
		if err != nil {
			return fmt.Errorf("creating request %v %v: %v", m, u.Path, err)
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

		resp, err := client.Do(req)
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
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxUploadRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Device %s: error uploading output: %v", dStr, err)
			log.Printf("Device %s: retrying in %s seconds\n", dStr, t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Device %s: error uploading output: %v", dStr, err)
		return
	}

	if err := checkContextCanceled(ctx); err != nil {
		log.Printf("Device %s: miner canceled job execution: %v", dStr, err)
		return
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

		m = "POST"
		p = path.Join("job", jID, "data")
		u.Path = p
		req, err := http.NewRequest(m, u.String(), pr)
		if err != nil {
			return fmt.Errorf("creating request %v %v: %v", m, u.Path, err)
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
		req = req.WithContext(ctx)

		log.Printf("Device %s: Uploading output...\n", dStr)
		resp, err := client.Do(req)
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
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxUploadRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Device %s: error uploading output: %v", dStr, err)
			log.Printf("Device %s: retrying in %s seconds\n", dStr, t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Device %s: error uploading output: %v", dStr, err)
		return
	}

	log.Printf("Device %s: Job completed!\n", dStr)
}
