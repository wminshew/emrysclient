package cmd

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

func (w *worker) executeJob(ctx context.Context, client *http.Client, u url.URL, mID, authToken, jID string) {
	w.busy = true
	w.jID = jID
	defer func() {
		w.busy = false
		w.jID = ""
	}()
	jobsInProcess++
	defer func() { jobsInProcess-- }()
	if err := checkContextCanceled(ctx); err != nil {
		log.Printf("Miner canceled job search: %v", err)
		return
	}
	w.miner.stop()
	defer w.miner.start()

	cli, err := docker.NewEnvClient()
	if err != nil {
		log.Printf("Error creating docker client: %v", err)
		return
	}
	defer check.Err(cli.Close)

	currUser, err := user.Current()
	if err != nil {
		log.Printf("Error getting current user: %v", err)
		return
	}
	if os.Geteuid() == 0 {
		currUser, err = user.Lookup(os.Getenv("SUDO_USER"))
		if err != nil {
			log.Printf("Error getting current sudo user: %v", err)
			return
		}
	}
	jobDir := filepath.Join(currUser.HomeDir, ".emrys", jID)
	if err = os.MkdirAll(jobDir, 0755); err != nil {
		log.Printf("Error making job dir %v: %v\n", jobDir, err)
		return
	}
	defer check.Err(func() error { return os.RemoveAll(jobDir) })

	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	registry := "registry.emrys.io"
	repo := "miner"
	imgRefStr := fmt.Sprintf("%s/%s/%s:latest", registry, repo, jID)
	go downloadImage(ctx, &wg, errCh, cli, imgRefStr)
	defer func() {
		if _, err := cli.ImageRemove(ctx, imgRefStr, types.ImageRemoveOptions{
			Force: true,
		}); err != nil {
			log.Printf("Error removing job image %v: %v\n", jID, err)
		}
		if _, err := cli.BuildCachePrune(ctx); err != nil {
			log.Printf("Error pruning build cache: %v", err)
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
		log.Printf("Miner canceled job search: %v", err)
		return
	}

	fileInfos, err := ioutil.ReadDir(jobDir)
	if err != nil {
		log.Printf("Error reading job dir %s: %v\n", jobDir, err)
		return
	}
	var hostDataDir string
	if len(fileInfos) > 0 {
		hostDataDir = filepath.Join(jobDir, fileInfos[0].Name())
	} else {
		hostDataDir = filepath.Join(jobDir, "data")
		if _, err := os.Create(hostDataDir); err != nil {
			log.Printf("Error creating empty data dir %s: %v\n", hostDataDir, err)
			return
		}
	}

	hostOutputDir := filepath.Join(jobDir, "output")
	oldUMask := syscall.Umask(000)
	if err = os.Chmod(hostDataDir, 0777); err != nil {
		log.Printf("Error modifying data dir %v permissions: %v\n", hostDataDir, err)
		_ = syscall.Umask(oldUMask)
		return
	}
	if err = os.MkdirAll(hostOutputDir, 0777); err != nil {
		log.Printf("Error making output dir %v: %v\n", hostOutputDir, err)
		_ = syscall.Umask(oldUMask)
		return
	}
	_ = syscall.Umask(oldUMask)

	userHome := "/home/user"
	dockerDataDir := filepath.Join(userHome, filepath.Base(hostDataDir))
	dockerOutputDir := filepath.Join(userHome, "output")
	dStr := strconv.Itoa(int(w.device))
	if err = os.Setenv("NVIDIA_VISIBLE_DEVICES", dStr); err != nil {
		log.Printf("Error setting NVIDIA_VISIBLE_DEVICES=%s: %v\n", dStr, err)
		return
	}
	defer check.Err(func() error { return os.Unsetenv("NVIDIA_VISIBLE_DEVICES") })
	c, err := cli.ContainerCreate(ctx, &container.Config{
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
		log.Printf("Error creating container: %v", err)
		return
	}
	if err := checkContextCanceled(ctx); err != nil {
		log.Printf("Miner canceled job search: %v", err)
		return
	}

	log.Printf("Running container...\n")
	if err := cli.ContainerStart(ctx, c.ID, types.ContainerStartOptions{}); err != nil {
		log.Printf("Error starting container: %v", err)
		return
	}

	out, err := cli.ContainerLogs(ctx, c.ID, types.ContainerLogsOptions{
		Follow:     true,
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		log.Printf("Error logging container: %v", err)
		return
	}
	defer check.Err(out.Close)

	maxUploadRetries := uint64(10)
	body := make([]byte, 4096)
	m := "POST"
	p := path.Join("job", jID, "log")
	u.Path = p
	var n int
	var req *http.Request
	var resp *http.Response
	for n, err = out.Read(body); err == nil; n, err = out.Read(body) {
		if err := checkContextCanceled(ctx); err != nil {
			log.Printf("Miner canceled job search: %v", err)
			return
		}
		operation := func() error {
			req, err = http.NewRequest(m, u.String(), bytes.NewReader(body[:n]))
			if err != nil {
				return fmt.Errorf("creating request %v %v: %v", m, u.Path, err)
			}
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
			req = req.WithContext(ctx)

			resp, err = client.Do(req)
			if err != nil {
				return fmt.Errorf("%v %v: %v", m, u.Path, err)
			}
			defer check.Err(resp.Body.Close)

			if resp.StatusCode != http.StatusOK {
				b, _ := ioutil.ReadAll(resp.Body)
				return fmt.Errorf("server response: %s", b)
			}
			return nil
		}
		if err := backoff.RetryNotify(operation,
			backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxUploadRetries), ctx),
			func(err error, t time.Duration) {
				log.Printf("Error uploading output: %v", err)
				log.Printf("Trying again in %s seconds\n", t.Round(time.Second).String())
			}); err != nil {
			log.Printf("Error uploading output: %v", err)
			return
		}
	}
	if err != nil && err != io.EOF {
		log.Printf("Error reading log buffer: %v", err)
		return
	}

	operation := func() error {
		// POST with empty body signifies log upload complete
		req, err = http.NewRequest(m, u.String(), nil)
		if err != nil {
			return fmt.Errorf("creating request %v %v: %v", m, u.Path, err)
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

		resp, err = client.Do(req)
		if err != nil {
			return fmt.Errorf("%v %v: %v", m, u.Path, err)
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode != http.StatusOK {
			b, _ := ioutil.ReadAll(resp.Body)
			return fmt.Errorf("server response: %s", b)
		}
		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxUploadRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Error uploading output: %v", err)
			log.Printf("Trying again in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Error uploading output: %v", err)
		return
	}

	// TODO: do I really want miner uploading output when he/she cancels?
	// if err := checkContextCanceled(ctx); err != nil {
	// 	log.Printf("Miner canceled job search: %v", err)
	// 	return
	// }
	ctx = context.Background()
	operation = func() error {
		pr, pw := io.Pipe()
		go func() {
			defer check.Err(pw.Close)
			files, err := ioutil.ReadDir(hostOutputDir)
			outputFiles := make([]string, 0, len(files))
			if err != nil {
				log.Printf("Error uploading output: reading files in output directory %v: %v\n", hostOutputDir, err)
				return
			}
			for _, file := range files {
				outputFile := filepath.Join(hostOutputDir, file.Name())
				outputFiles = append(outputFiles, outputFile)
			}
			if err = archiver.TarGz.Write(pw, outputFiles); err != nil {
				log.Printf("Error uploading output: packing output directory %v: %v\n", hostOutputDir, err)
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

		log.Printf("Uploading output...\n")
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("%v %v: %v", m, u.Path, err)
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode != http.StatusOK {
			b, _ := ioutil.ReadAll(resp.Body)
			return fmt.Errorf("server response: %s", b)
		}
		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxUploadRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Error uploading output: %v", err)
			log.Printf("Trying again in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Error uploading output: %v", err)
		return
	}

	log.Printf("Job completed!\n")
}
