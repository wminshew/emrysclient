package cmd

import (
	"bytes"
	"context"
	"docker.io/go-docker"
	"docker.io/go-docker/api/types"
	"docker.io/go-docker/api/types/container"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/mholt/archiver"
	"github.com/spf13/viper"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/job"
	"github.com/wminshew/emrys/pkg/jsonmessage"
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

func bid(client *http.Client, u url.URL, mID, authToken string, msg *job.Message) {
	if err := checkVersion(client, u); err != nil {
		log.Printf("Version error: %v\n", err)
		return
	}
	jID := msg.Job.ID.String()

	var body bytes.Buffer
	b := &job.Bid{
		MinRate: viper.GetFloat64("bid-rate"),
	}
	if err := json.NewEncoder(&body).Encode(b); err != nil {
		log.Printf("Error encoding json bid: %v\n", err)
		return
	}
	m := "POST"
	p := path.Join("miner", "job", jID, "bid")
	u.Path = p
	req, err := http.NewRequest(m, u.String(), &body)
	if err != nil {
		log.Printf("Failed to create http request %v %v: %v\n", m, p, err)
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	log.Printf("Sending bid with rate: %v...\n", b.MinRate)
	var resp *http.Response
	operation := func() error {
		var err error
		resp, err = client.Do(req)
		return err
	}
	if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
		log.Printf("Error %v %v: %v\n", req.Method, req.URL.Path, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		if busy {
			log.Println("Bid rejected -- you're busy with another job!")
		} else if !busy && resp.StatusCode == http.StatusPaymentRequired {
			log.Println("Your bid was too low, maybe next time!")
		} else {
			log.Printf("Response error header: %v\n", resp.Status)
			b, _ := ioutil.ReadAll(resp.Body)
			log.Printf("Response error detail: %s\n", b)
			check.Err(resp.Body.Close)
		}
		return
	}
	check.Err(resp.Body.Close)
	log.Printf("You won job %v!\n", jID)
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
	go downloadImage(ctx, &wg, errCh, cli, jID)
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
	hostDataDir := filepath.Join(jobDir, fileInfos[0].Name())

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
		Image: jID,
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

	// TODO: store this output in some kind of buffer / file, so I can re-upload if connection is interrupted
	out, err := cli.ContainerLogs(ctx, c.ID, types.ContainerLogsOptions{
		Follow:     true,
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		log.Printf("Error logging container: %v\n", err)
		return
	}

	m = "POST"
	p = path.Join("job", jID, "output", "log")
	u.Path = p
	req, err = http.NewRequest(m, u.String(), out)
	if err != nil {
		log.Printf("Failed to create http request %v %v: %v\n", m, p, err)
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	resp, err = client.Do(req)
	if err != nil {
		log.Printf("Error %v %v: %v\n", req.Method, p, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("Response error header: %v\n", resp.Status)
		b, _ := ioutil.ReadAll(resp.Body)
		log.Printf("Response error detail: %s\n", b)
		check.Err(resp.Body.Close)
		return
	}

	check.Err(resp.Body.Close)

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
	p = path.Join("job", jID, "output", "dir")
	u.Path = p
	req, err = http.NewRequest(m, u.String(), pr)
	if err != nil {
		log.Printf("Failed to create http request %v %v: %v\n", m, p, err)
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	log.Printf("Uploading output...\n")
	resp, err = client.Do(req)
	if err != nil {
		log.Printf("Error %v %v: %v\n", req.Method, p, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("Response error header: %v\n", resp.Status)
		b, _ := ioutil.ReadAll(resp.Body)
		log.Printf("Response error detail: %s\n", b)
		check.Err(resp.Body.Close)
		return
	}

	check.Err(resp.Body.Close)
	log.Printf("Job completed!\n")
}

func downloadImage(ctx context.Context, wg *sync.WaitGroup, errCh chan<- error, cli *docker.Client, jID string) {
	defer wg.Done()
	log.Printf("Downloading image...\n")
	registry := "registry.emrys.io"
	repo := "miner"
	refStr := fmt.Sprintf("%s/%s/%s:latest", registry, repo, jID)
	pullResp, err := cli.ImagePull(ctx, refStr, types.ImagePullOptions{
		RegistryAuth: "none",
	})
	if err != nil {
		log.Printf("Error downloading image: %v\n", err)
		errCh <- err
		return
	}
	defer check.Err(pullResp.Close)

	if err := jsonmessage.DisplayJSONMessagesStream(pullResp, os.Stdout, os.Stdout.Fd(), nil); err != nil {
		log.Printf("Error downloading image: %v\n", err)
		errCh <- err
		return
	}
}

func downloadData(ctx context.Context, wg *sync.WaitGroup, errCh chan<- error, client *http.Client, u url.URL, jID, authToken, jobDir string) {
	defer wg.Done()
	m := "GET"
	p := path.Join("miner", "job", jID)
	u.Host = "data.emrys.io"
	u.Path = p
	req, err := http.NewRequest(m, u.String(), nil)
	if err != nil {
		log.Printf("Failed to create http request %v %v: %v\n", m, p, err)
		errCh <- err
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	log.Printf("Downloading data...\n")
	var resp *http.Response
	operation := func() error {
		var err error
		resp, err = client.Do(req)
		return err
	}
	if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
		log.Printf("Error %v %v: %v\n", req.Method, req.URL.Path, err)
		errCh <- err
		return
	}
	defer check.Err(resp.Body.Close)

	if resp.StatusCode != http.StatusOK {
		log.Printf("Response error header: %v\n", resp.Status)
		b, _ := ioutil.ReadAll(resp.Body)
		log.Printf("Response error detail: %s\n", b)
		errCh <- fmt.Errorf("%v", b)
		return
	}

	if err = archiver.TarGz.Read(resp.Body, jobDir); err != nil {
		log.Printf("Error unpacking .tar.gz into job dir %v: %v\n", jobDir, err)
		errCh <- err
		return
	}
	log.Printf("Data downloaded!\n")
}
