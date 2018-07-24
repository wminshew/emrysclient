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
	"github.com/gorilla/websocket"
	"github.com/mholt/archiver"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/job"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"syscall"
	"time"
)

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
			fmt.Printf("Error getting authToken: %v\n", err)
			return
		}
		claims := &jwt.StandardClaims{}
		if _, _, err := new(jwt.Parser).ParseUnverified(authToken, claims); err != nil {
			fmt.Printf("Error parsing authToken %v: %v\n", authToken, err)
			return
		}
		if err := claims.Valid(); err != nil {
			fmt.Printf("Error invalid authToken claims: %v\n", err)
			fmt.Printf("Please login again.\n")
			return
		}
		mID := claims.Subject
		exp := claims.ExpiresAt
		if remaining := time.Until(time.Unix(exp, 0)); remaining <= 24*time.Hour {
			fmt.Printf("Warning: login token expires in apprx. ~%.f hours\n", remaining.Hours())
		}

		if err := checkVersion(); err != nil {
			fmt.Printf("Version error: %v\n", err)
			return
		}

		viper.SetConfigName(viper.GetString("config"))
		viper.AddConfigPath(".")
		if err := viper.ReadInConfig(); err != nil {
			fmt.Printf("Error reading config file: %v\n", err)
			return
		}
		viper.WatchConfig()
		viper.OnConfigChange(func(e fsnotify.Event) {
			fmt.Printf("Config file changed: %v %v\n", e.Op, e.Name)
		})

		var conn *websocket.Conn
		operation := func() error {
			var err error
			conn, _, err = dialWebsocket(mID, authToken)
			return err
		}
		expBackOff := backoff.NewExponentialBackOff()
		if err := backoff.Retry(operation, expBackOff); err != nil {
			fmt.Printf("Error dialing websocket: %v\n", err)
			return
		}
		defer check.Err(conn.Close)

		response := make(chan []byte)
		done := make(chan struct{})
		interrupt := make(chan os.Signal, 1)

		go func() {
			defer close(done)
			for {
				msgType, r, err := conn.NextReader()
				if err != nil {
					fmt.Printf("Error reading message: %v\n", err)
					return
				}
				switch msgType {
				case websocket.BinaryMessage:
					msg := &job.Message{}
					if err := json.NewDecoder(r).Decode(msg); err != nil {
						fmt.Printf("Error decoding json message: %v\n", err)
						break
					}
					fmt.Printf("%v\n", msg.Message)
					if msg.Job == nil {
						break
					}
					fmt.Printf("Job: %v\n", msg.Job.ID.String())

					go bid(mID, authToken, msg)
				case websocket.TextMessage:
					fmt.Printf("Error -- unexpected text message received.\n")
					_, err = io.Copy(os.Stdout, r)
					response <- []byte("pong")
				default:
					fmt.Printf("Non-text or -binary websocket message received. Closing.\n")
					return
				}
			}
		}()

		for {
			select {
			case <-done:
				return
			case r := <-response:
				err := conn.WriteMessage(websocket.TextMessage, r)
				if err != nil {
					fmt.Printf("Error writing message: %v\n", err)
					return
				}
			case <-interrupt:
				fmt.Println("interrupt")

				err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				if err != nil {
					fmt.Printf("Error writing close: %v\n", err)
					return
				}
				select {
				case <-done:
				case <-time.After(time.Second):
				}
				return
			}
		}
	},
}

func dialWebsocket(mID, t string) (*websocket.Conn, *http.Response, error) {
	s := "wss"
	h := resolveHost()
	p := path.Join("miner", mID, "connect")
	u := url.URL{
		Scheme: s,
		Host:   h,
		Path:   p,
	}
	fmt.Printf("Connecting to emrys...\n")
	// o := url.URL{
	// 	Scheme: "https",
	// 	Host: h,
	// }
	d := websocket.DefaultDialer
	reqH := http.Header{}
	reqH.Set("Authorization", fmt.Sprintf("Bearer %v", t))
	// reqH.Set("Origin", o.String())
	return d.Dial(u.String(), reqH)
}

func bid(mID, authToken string, msg *job.Message) {
	if err := checkVersion(); err != nil {
		fmt.Printf("Version error: %v\n", err)
		return
	}

	client := http.Client{}
	b := &job.Bid{
		MinRate: viper.GetFloat64("bid-rate"),
	}

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(b); err != nil {
		fmt.Printf("Error encoding json bid: %v\n", err)
		return
	}
	m := "POST"
	s := "https"
	h := resolveHost()
	p := path.Join("miner", mID, "job", msg.Job.ID.String(), "bid")
	u := url.URL{
		Scheme: s,
		Host:   h,
		Path:   p,
	}
	req, err := http.NewRequest(m, u.String(), &body)
	if err != nil {
		fmt.Printf("Failed to create http request %v %v: %v\n", m, p, err)
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	fmt.Printf("Sending bid with rate: %v...\n", b.MinRate)
	var resp *http.Response
	operation := func() error {
		var err error
		resp, err = client.Do(req)
		return err
	}
	expBackOff := backoff.NewExponentialBackOff()
	if err := backoff.Retry(operation, expBackOff); err != nil {
		fmt.Printf("Error %v %v: %v\n", req.Method, req.URL.Path, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Response error header: %v\n", resp.Status)
		b, _ := ioutil.ReadAll(resp.Body)
		fmt.Printf("Response error detail: %s\n", b)
		check.Err(resp.Body.Close)
		return
	}
	check.Err(resp.Body.Close)
	fmt.Printf("Your bid for job %v was selected!\n", msg.Job.ID.String())

	m = "GET"
	p = path.Join("image", msg.Job.ID.String())
	u = url.URL{
		Scheme: s,
		Host:   h,
		Path:   p,
	}
	req, err = http.NewRequest(m, u.String(), nil)
	if err != nil {
		fmt.Printf("Failed to create http request %v %v: %v\n", m, p, err)
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	// TODO: replace with docker pull
	// TODO: make parallel with data sync
	fmt.Printf("Downloading image...\n")
	// var resp *http.Response
	operation = func() error {
		var err error
		resp, err = client.Do(req)
		return err
	}
	expBackOff = backoff.NewExponentialBackOff()
	if err := backoff.Retry(operation, expBackOff); err != nil {
		fmt.Printf("Error %v %v: %v\n", req.Method, req.URL.Path, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Response header error: %v\n", resp.Status)
		check.Err(resp.Body.Close)
		return
	}

	zResp, err := zlib.NewReader(resp.Body)
	if err != nil {
		fmt.Printf("Error creating zlib img reader: %v\n", err)
		check.Err(resp.Body.Close)
		return
	}

	ctx := context.Background()
	// TODO: create docker client before connecting websocket
	cli, err := docker.NewEnvClient()
	if err != nil {
		fmt.Printf("Error creating docker client: %v\n", err)
		check.Err(resp.Body.Close)
		check.Err(zResp.Close)
		return
	}
	imgLoadResp, err := cli.ImageLoad(ctx, zResp, false)
	if err != nil {
		fmt.Printf("Error loading image: %v\n", err)
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
	// 		fmt.Printf("Error deleting image %v: %v\n", msg.Job.ID.String(), err)
	// 		return
	// 	}
	// 	for i := range imgDelResp {
	// 		fmt.Printf("Deleted: %v", imgDelResp[i].Deleted)
	// 		fmt.Printf("Untagged: %v", imgDelResp[i].Untagged)
	// 	}
	// }()
	check.Err(zResp.Close)
	check.Err(resp.Body.Close)

	user, err := user.Current()
	if err != nil {
		fmt.Printf("Error getting current user: %v\n", err)
		return
	}
	jobDir := filepath.Join(user.HomeDir, ".emrys", msg.Job.ID.String())
	if err = os.MkdirAll(jobDir, 0755); err != nil {
		fmt.Printf("Error making job dir %v: %v\n", jobDir, err)
		return
	}
	defer check.Err(func() error { return os.RemoveAll(jobDir) })

	m = "GET"
	p = path.Join("data", msg.Job.ID.String())
	u = url.URL{
		Scheme: s,
		Host:   h,
		Path:   p,
	}
	req, err = http.NewRequest(m, u.String(), nil)
	if err != nil {
		fmt.Printf("Failed to create http request %v %v: %v\n", m, p, err)
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	fmt.Printf("Downloading data...\n")
	// var resp *http.Response
	operation = func() error {
		var err error
		resp, err = client.Do(req)
		return err
	}
	expBackOff = backoff.NewExponentialBackOff()
	if err := backoff.Retry(operation, expBackOff); err != nil {
		fmt.Printf("Error %v %v: %v\n", req.Method, req.URL.Path, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Response header error: %v\n", resp.Status)
		check.Err(resp.Body.Close)
		return
	}

	if err = archiver.TarGz.Read(resp.Body, jobDir); err != nil {
		fmt.Printf("Error unpacking .tar.gz into job dir %v: %v\n", jobDir, err)
		check.Err(resp.Body.Close)
		return
	}
	check.Err(resp.Body.Close)

	fileInfos, err := ioutil.ReadDir(jobDir)
	hostDataDir := filepath.Join(jobDir, fileInfos[0].Name())

	hostOutputDir := filepath.Join(jobDir, "output")
	oldUMask := syscall.Umask(000)
	if err = os.Chmod(hostDataDir, 0777); err != nil {
		fmt.Printf("Error modifying data dir %v permissions: %v\n", hostDataDir, err)
		_ = syscall.Umask(oldUMask)
		return
	}
	if err = os.MkdirAll(hostOutputDir, 0777); err != nil {
		fmt.Printf("Error making output dir %v: %v\n", hostOutputDir, err)
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
		fmt.Printf("Error creating container: %v\n", err)
		return
	}

	fmt.Printf("Running container...\n")
	if err := cli.ContainerStart(ctx, c.ID, types.ContainerStartOptions{}); err != nil {
		fmt.Printf("Error starting container: %v\n", err)
		return
	}

	// TODO: store this output in some kind of buffer / file, so I can re-upload if connection is interrupted
	out, err := cli.ContainerLogs(ctx, c.ID, types.ContainerLogsOptions{
		Follow:     true,
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		fmt.Printf("Error logging container: %v\n", err)
		return
	}

	m = "POST"
	p = path.Join("job", msg.Job.ID.String(), "output", "log")
	u = url.URL{
		Scheme: s,
		Host:   h,
		Path:   p,
	}
	req, err = http.NewRequest(m, u.String(), out)
	if err != nil {
		fmt.Printf("Failed to create http request %v %v: %v\n", m, p, err)
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	resp, err = client.Do(req)
	if err != nil {
		fmt.Printf("Error %v %v: %v\n", req.Method, p, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Response header error: %v\n", resp.Status)
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
			fmt.Printf("Error reading files in hostOutputDir %v: %v\n", hostOutputDir, err)
			return
		}
		for _, file := range files {
			outputFile := filepath.Join(hostOutputDir, file.Name())
			outputFiles = append(outputFiles, outputFile)
		}
		if err = archiver.TarGz.Write(pw, outputFiles); err != nil {
			fmt.Printf("Error packing output dir %v: %v\n", hostOutputDir, err)
			return
		}
	}()
	m = "POST"
	p = path.Join("job", msg.Job.ID.String(), "output", "dir")
	u = url.URL{
		Scheme: s,
		Host:   h,
		Path:   p,
	}
	req, err = http.NewRequest(m, u.String(), pr)
	if err != nil {
		fmt.Printf("Failed to create http request %v %v: %v\n", m, p, err)
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	fmt.Printf("Uploading output...\n")
	resp, err = client.Do(req)
	if err != nil {
		fmt.Printf("Error %v %v: %v\n", req.Method, p, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Response header error: %v\n", resp.Status)
		check.Err(resp.Body.Close)
		return
	}

	check.Err(resp.Body.Close)
	fmt.Printf("Job completed!\n")
}
