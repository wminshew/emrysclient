package cmd

import (
	"bytes"
	"compress/zlib"
	"context"
	"docker.io/go-docker"
	"docker.io/go-docker/api/types"
	"docker.io/go-docker/api/types/container"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/dgrijalva/jwt-go"
	"github.com/fsnotify/fsnotify"
	"github.com/mholt/archiver"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/job"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"syscall"
	"time"
)

type pollResponse struct {
	Events    []pollEvent `json:"events"`
	Timestamp int64       `json:"timestamp"`
}

// source: https://github.com/jcuga/golongpoll/blob/master/go-client/glpclient/client.go
type pollEvent struct {
	// Timestamp is milliseconds since epoch to match javascrits Date.getTime()
	Timestamp int64  `json:"timestamp"`
	Category  string `json:"category"`
	// Data can be anything that is able to passed to json.Marshal()
	Data json.RawMessage `json:"data"`
}

var busy = false

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Begin mining on emrys",
	Long: "Start executing deep learning jobs for money. " +
		"When no jobs are available, or if the asking rates are " +
		"below your minimum, emrysminer will default to the mining " +
		"command provided in ./mining-script.sh.",
	Run: func(cmd *cobra.Command, args []string) {
		authToken, err := getToken()
		if err != nil {
			log.Printf("Error getting authToken: %v\n", err)
			return
		}
		claims := &jwt.StandardClaims{}
		if _, _, err := new(jwt.Parser).ParseUnverified(authToken, claims); err != nil {
			log.Printf("Error parsing authToken %v: %v\n", authToken, err)
			return
		}
		if err := claims.Valid(); err != nil {
			log.Printf("Error invalid authToken claims: %v\n", err)
			log.Printf("Please login again.\n")
			return
		}
		mID := claims.Subject
		exp := claims.ExpiresAt
		if remaining := time.Until(time.Unix(exp, 0)); remaining <= 24*time.Hour {
			log.Printf("Warning: login token expires in apprx. ~%.f hours\n", remaining.Hours())
		}

		tr := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   60 * time.Second,
				KeepAlive: 60 * time.Second,
				DualStack: true,
			}).DialContext,
			MaxIdleConns:          20,
			IdleConnTimeout:       10 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DisableCompression:    true,
		}
		client := &http.Client{Transport: tr}
		s := "https"
		h := resolveHost()
		u := url.URL{
			Scheme: s,
			Host:   h,
		}
		if err := checkVersion(client, u); err != nil {
			log.Printf("Version error: %v\n", err)
			return
		}

		viper.SetConfigName(viper.GetString("config"))
		viper.AddConfigPath(".")
		if err := viper.ReadInConfig(); err != nil {
			log.Printf("Error reading config file: %v\n", err)
			return
		}
		viper.WatchConfig()
		viper.OnConfigChange(func(e fsnotify.Event) {
			log.Printf("Config file changed: %v %v\n", e.Op, e.Name)
		})

		m := "GET"
		p := path.Join("miner", "connect")
		u.Path = p
		q := u.Query()
		q.Set("timeout", "600")
		sinceTime := time.Now().Unix() * 1000
		q.Set("since_time", fmt.Sprintf("%d", sinceTime))
		u.RawQuery = q.Encode()

		// could use: https://github.com/jcuga/golongpoll/tree/master/go-client/glpclient
		var req *http.Request
		var resp *http.Response
		var operation backoff.Operation
		for {
			if !busy {
				log.Printf("Pinging emrys for jobs...\n")
				operation = func() error {
					if req, err = http.NewRequest(m, u.String(), nil); err != nil {
						log.Printf("Failed to create http request %v %v: %v\n", m, p, err)
						return err
					}
					req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

					resp, err = client.Do(req)
					return err
				}
				if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
					log.Printf("Error %v %v: %v\n", req.Method, req.URL.Path, err)
					log.Printf("Unable to connect to emrys. Retrying in 5 minutes...\n")
					time.Sleep(5 * time.Minute)
					continue
				}

				if resp.StatusCode != http.StatusOK {
					log.Printf("Response error header: %v\n", resp.Status)
					b, _ := ioutil.ReadAll(resp.Body)
					log.Printf("Response error detail: %s\n", b)
					check.Err(resp.Body.Close)
					continue
				}

				pr := pollResponse{}
				if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
					log.Printf("Error decoding json pollResponse: %v\n", err)
					check.Err(resp.Body.Close)
					continue
				}
				check.Err(resp.Body.Close)

				if len(pr.Events) > 0 {
					log.Println(len(pr.Events), "job(s) up for auction")
					for _, event := range pr.Events {
						sinceTime = event.Timestamp
						msg := &job.Message{}
						if err := json.Unmarshal(event.Data, msg); err != nil {
							log.Printf("Error unmarshaling json message: %v\n", err)
							log.Println("json message: ", string(event.Data))
							continue
						}
						if msg.Job == nil {
							continue
						}
						go bid(client, u, mID, authToken, msg)
					}
				} else {
					if pr.Timestamp > sinceTime {
						sinceTime = pr.Timestamp
					}
				}

				q = u.Query()
				q.Set("since_time", fmt.Sprintf("%d", sinceTime))
				u.RawQuery = q.Encode()
			} else {
				// wait until finished with job
				// TODO: block this with a channel to reduce cpu load
				time.Sleep(10 * time.Second)
			}
		}
	},
}

func bid(client *http.Client, u url.URL, mID, authToken string, msg *job.Message) {
	if err := checkVersion(client, u); err != nil {
		log.Printf("Version error: %v\n", err)
		return
	}

	var body bytes.Buffer
	b := &job.Bid{
		MinRate: viper.GetFloat64("bid-rate"),
	}
	if err := json.NewEncoder(&body).Encode(b); err != nil {
		log.Printf("Error encoding json bid: %v\n", err)
		return
	}
	m := "POST"
	p := path.Join("miner", "job", msg.Job.ID.String(), "bid")
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
	log.Printf("You won job %v!\n", msg.Job.ID.String())
	busy = true
	defer func() { busy = false }()

	m = "GET"
	p = path.Join("image", msg.Job.ID.String())
	u.Path = p
	req, err = http.NewRequest(m, u.String(), nil)
	if err != nil {
		log.Printf("Failed to create http request %v %v: %v\n", m, p, err)
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	// TODO: replace with docker pull
	// TODO: make parallel with data sync
	log.Printf("Downloading image...\n")
	// var resp *http.Response
	operation = func() error {
		var err error
		resp, err = client.Do(req)
		return err
	}
	if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
		log.Printf("Error %v %v: %v\n", req.Method, req.URL.Path, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("Response error header: %v\n", resp.Status)
		b, _ := ioutil.ReadAll(resp.Body)
		log.Printf("Response error detail: %s\n", b)
		check.Err(resp.Body.Close)
		return
	}

	zResp, err := zlib.NewReader(resp.Body)
	if err != nil {
		log.Printf("Error creating zlib img reader: %v\n", err)
		check.Err(resp.Body.Close)
		return
	}

	ctx := context.Background()
	// TODO: create docker client before connecting websocket
	cli, err := docker.NewEnvClient()
	if err != nil {
		log.Printf("Error creating docker client: %v\n", err)
		check.Err(resp.Body.Close)
		check.Err(zResp.Close)
		return
	}
	imgLoadResp, err := cli.ImageLoad(ctx, zResp, false)
	if err != nil {
		log.Printf("Error loading image: %v\n", err)
		check.Err(resp.Body.Close)
		check.Err(zResp.Close)
		return
	}
	defer check.Err(imgLoadResp.Body.Close)
	// TODO:
	// defer func() {
	// 	imgDelResp, err := cli.ImageRemove(ctx, msg.Job.ID.String(), types.ImageRemoveOptions{
	// 		Force: true,
	// 	})
	// 	if err != nil {
	// 		log.Printf("Error deleting image %v: %v\n", msg.Job.ID.String(), err)
	// 		return
	// 	}
	// 	for i := range imgDelResp {
	// 		log.Printf("Deleted: %v", imgDelResp[i].Deleted)
	// 		log.Printf("Untagged: %v", imgDelResp[i].Untagged)
	// 	}
	// }()
	check.Err(zResp.Close)
	check.Err(resp.Body.Close)

	user, err := user.Current()
	if err != nil {
		log.Printf("Error getting current user: %v\n", err)
		return
	}
	jobDir := filepath.Join(user.HomeDir, ".emrys", msg.Job.ID.String())
	if err = os.MkdirAll(jobDir, 0755); err != nil {
		log.Printf("Error making job dir %v: %v\n", jobDir, err)
		return
	}
	defer check.Err(func() error { return os.RemoveAll(jobDir) })

	m = "GET"
	p = path.Join("data", msg.Job.ID.String())
	u.Path = p
	req, err = http.NewRequest(m, u.String(), nil)
	if err != nil {
		log.Printf("Failed to create http request %v %v: %v\n", m, p, err)
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	log.Printf("Downloading data...\n")
	// var resp *http.Response
	operation = func() error {
		var err error
		resp, err = client.Do(req)
		return err
	}
	if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
		log.Printf("Error %v %v: %v\n", req.Method, req.URL.Path, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("Response error header: %v\n", resp.Status)
		b, _ := ioutil.ReadAll(resp.Body)
		log.Printf("Response error detail: %s\n", b)
		check.Err(resp.Body.Close)
		return
	}

	if err = archiver.TarGz.Read(resp.Body, jobDir); err != nil {
		log.Printf("Error unpacking .tar.gz into job dir %v: %v\n", jobDir, err)
		check.Err(resp.Body.Close)
		return
	}
	check.Err(resp.Body.Close)

	fileInfos, err := ioutil.ReadDir(jobDir)
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
		Image: msg.Job.ID.String(),
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
	p = path.Join("job", msg.Job.ID.String(), "output", "log")
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
	p = path.Join("job", msg.Job.ID.String(), "output", "dir")
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
