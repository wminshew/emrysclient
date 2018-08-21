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
	"sync"
	"syscall"
)

func executeJob(client *http.Client, u url.URL, mID, authToken, jID string) {
	busy = true
	defer func() { busy = false }()

	ctx := context.Background()
	cli, err := docker.NewEnvClient()
	if err != nil {
		log.Printf("Error creating docker client: %v\n", err)
		return
	}
	defer check.Err(cli.Close)
	user, err := user.Current()
	if err != nil {
		log.Printf("Error getting current user: %v\n", err)
		return
	}
	jobDir := filepath.Join(user.HomeDir, ".emrys", jID)
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
		if _, err := cli.ImageRemove(ctx, imgRefStr, types.ImageRemoveOptions{}); err != nil {
			log.Printf("Error removing job image %v: %v\n", jID, err)
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
		// ReadonlyRootfs: true,
		Runtime: "nvidia",
		SecurityOpt: []string{
			"no-new-privileges",
		},
	}, nil, "")
	if err != nil {
		log.Printf("Error creating container: %v\n", err)
		return
	}

	log.Printf("Running container...\n")
	if err := cli.ContainerStart(ctx, c.ID, types.ContainerStartOptions{}); err != nil {
		log.Printf("Error starting container: %v\n", err)
		return
	}

	out, err := cli.ContainerLogs(ctx, c.ID, types.ContainerLogsOptions{
		Follow:     true,
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		log.Printf("Error logging container: %v\n", err)
		return
	}

	var buf bytes.Buffer
	go func() {
		if _, err := io.Copy(&buf, out); err != nil {
			log.Printf("Error copying log to buffer: %v\n", err)
			return
		}
	}()

	body := make([]byte, 8192)
	m := "POST"
	p := path.Join("job", jID, "log")
	u.Path = p
	var n int
	var req *http.Request
	var resp *http.Response
	for n, err = buf.Read(body); err == nil; n, err = buf.Read(body) {
		operation := func() error {
			req, err = http.NewRequest(m, u.String(), bytes.NewReader(body[:n]))
			if err != nil {
				log.Printf("error creating http request %v %v: %v\n", m, p, err)
				return err
			}
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

			resp, err = client.Do(req)
			if err != nil {
				log.Printf("Error %v %v: %v\n", req.Method, p, err)
				return err
			}
			defer check.Err(resp.Body.Close)

			if resp.StatusCode != http.StatusOK {
				log.Printf("http request error %v %v\n", m, p)
				log.Printf("Response header: %v\n", resp.Status)
				b, _ := ioutil.ReadAll(resp.Body)
				log.Printf("Response detail: %s", b)
				return fmt.Errorf("%s", b)
			}
			return nil
		}
		if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
			log.Printf("Error %v %v: %v\n", req.Method, req.URL.Path, err)
			return
		}
	}
	if err != nil && err != io.EOF {
		log.Printf("Error reading log buffer: %v\n", err)
		return
	}

	operation := func() error {
		var err error
		pr, pw := io.Pipe()
		go func() {
			defer check.Err(pw.Close)
			files, err := ioutil.ReadDir(hostOutputDir)
			outputFiles := make([]string, 0, len(files))
			if err != nil {
				log.Printf("Error reading files in hostOutputDir %v: %v\n", hostOutputDir, err)
				return
			}
			for _, file := range files {
				outputFile := filepath.Join(hostOutputDir, file.Name())
				outputFiles = append(outputFiles, outputFile)
			}
			if err = archiver.TarGz.Write(pw, outputFiles); err != nil {
				log.Printf("Error packing output dir %v: %v\n", hostOutputDir, err)
				return
			}
		}()

		m = "POST"
		p = path.Join("job", jID, "dir")
		u.Path = p
		req, err = http.NewRequest(m, u.String(), pr)
		if err != nil {
			log.Printf("Failed to create http request %v %v: %v\n", m, p, err)
			return err
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

		log.Printf("Uploading output...\n")
		resp, err = client.Do(req)
		if err != nil {
			log.Printf("Error %v %v: %v\n", req.Method, p, err)
			return err
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode != http.StatusOK {
			log.Printf("http request error %v %v\n", m, p)
			log.Printf("Response header: %v\n", resp.Status)
			b, _ := ioutil.ReadAll(resp.Body)
			log.Printf("Response detail: %s", b)
			return fmt.Errorf("%s", b)
		}
		return nil
	}
	if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
		log.Printf("Error %v %v: %v\n", req.Method, req.URL.Path, err)
		return
	}

	log.Printf("Job completed!\n")
}
