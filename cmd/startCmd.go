package cmd

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"context"
	"docker.io/go-docker"
	"docker.io/go-docker/api/types"
	"docker.io/go-docker/api/types/container"
	"encoding/json"
	"fmt"
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
	"log"
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
	Long: `Start executing deep learning jobs
for money. When no jobs are available, or if the
asking rates are below your minimum, emrysminer
will default to the mining command provided in
./mining-script.sh.`,
	Run: func(cmd *cobra.Command, args []string) {
		authToken := getToken()
		claims := &jwt.StandardClaims{}
		_, _, err := new(jwt.Parser).ParseUnverified(authToken, claims)
		if err != nil {
			log.Printf("Error parsing authToken %v: %v\n", authToken, err)
			return
		}
		if err = claims.Valid(); err != nil {
			log.Printf("Error invalid authToken claims: %v\n", err)
			log.Printf("Please login again.\n")
			return
		}
		mID := claims.Subject

		viper.SetConfigName(viper.GetString("config"))
		viper.AddConfigPath(".")
		err = viper.ReadInConfig()
		if err != nil {
			log.Printf("Error reading config file: %v\n", err)
			return
		}
		viper.WatchConfig()
		viper.OnConfigChange(func(e fsnotify.Event) {
			log.Printf("Config file changed: %v %v\n", e.Op, e.Name)
		})

		conn, _, err := dialWebsocket(mID, authToken)
		if err != nil {
			log.Printf("Error dialing websocket: %v\n", err)
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
					log.Printf("Error reading message: %v\n", err)
					return
				}
				switch msgType {
				case websocket.BinaryMessage:
					m := &job.Message{}
					err = json.NewDecoder(r).Decode(m)
					if err != nil {
						log.Printf("Error decoding json message: %v\n", err)
						break
					}
					log.Printf("Message: %v\n", m.Message)
					if m.Job == nil {
						break
					}
					log.Printf("Job: %v\n", m.Job.ID.String())

					go bid(mID, authToken, m)
				case websocket.TextMessage:
					resp := "Error -- unexpected text message received.\n"
					log.Printf(resp)
					_, err = io.Copy(os.Stdout, r)
					response <- []byte(resp)
				default:
					log.Printf("Non-text or -binary websocket message received. Closing.\n")
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
					log.Printf("Error writing message: %v\n", err)
					return
				}
			case <-interrupt:
				log.Println("interrupt")

				err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				if err != nil {
					log.Printf("Error writing close: %v\n", err)
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
	h := resolveHost()

	p := path.Join("miner", mID, "connect")
	u := url.URL{
		Scheme: "wss",
		Host:   h,
		Path:   p,
	}
	log.Printf("Connecting to %s...\n", u.String())
	o := url.URL{
		Scheme: "https",
		Host:   h,
	}
	d := websocket.DefaultDialer
	d.TLSClientConfig = resolveTLSConfig()
	reqH := http.Header{}
	reqH.Set("Authorization", fmt.Sprintf("Bearer %v", t))
	reqH.Set("Origin", o.String())
	return d.Dial(u.String(), reqH)
}

func postReq(path, authToken string, body io.Reader) (*http.Request, error) {
	h := resolveHost()
	u := url.URL{
		Scheme: "https",
		Host:   h,
		Path:   path,
	}
	req, err := http.NewRequest("POST", u.String(), body)
	if err != nil {
		log.Printf("Failed to create new http request: %v\n", err)
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	return req, nil
}

func getJobReq(path, authToken, jobToken string) (*http.Request, error) {
	h := resolveHost()
	u := url.URL{
		Scheme: "https",
		Host:   h,
		Path:   path,
	}
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		log.Printf("Failed to create new http request: %v\n", err)
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
	req.Header.Set("Job-Authorization", jobToken)

	return req, nil
}

func postJobReq(path, authToken, jobToken string, body io.Reader) (*http.Request, error) {
	h := resolveHost()
	u := url.URL{
		Scheme: "https",
		Host:   h,
		Path:   path,
	}
	req, err := http.NewRequest("POST", u.String(), body)
	if err != nil {
		log.Printf("Failed to create new http request: %v\n", err)
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
	req.Header.Set("Job-Authorization", jobToken)

	return req, nil
}

func bid(mID, authToken string, m *job.Message) {
	client := resolveClient()
	b := &job.Bid{
		MinRate: viper.GetFloat64("bid-rate"),
	}
	log.Printf("Sending bid with rate: %v\n", b.MinRate)

	var body bytes.Buffer
	jobPath := path.Join("miner", mID, "job", m.Job.ID.String())
	p := path.Join(jobPath, "bid")
	err := json.NewEncoder(&body).Encode(b)
	if err != nil {
		log.Printf("Error encoding json bid: %v\n", err)
		return
	}
	req, err := postReq(p, authToken, &body)
	if err != nil {
		log.Printf("Error creating request POST %v: %v\n", p, err)
		return
	}
	log.Printf("%v %v\n", req.Method, p)
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error %v %v: %v\n", req.Method, p, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("Response header error: %v\n", resp.Status)
		check.Err(resp.Body.Close)
		return
	}

	jobToken := resp.Header.Get("Set-Job-Authorization")
	if jobToken == "" {
		s := bufio.NewScanner(resp.Body)
		for s.Scan() {
			log.Println(s.Text())
		}
		log.Printf("Your bid for job %v was not selected.\n", m.Job.ID.String())
		check.Err(resp.Body.Close)
		return
	}

	_, _ = io.Copy(ioutil.Discard, resp.Body)
	check.Err(resp.Body.Close)

	log.Printf("Your bid for job %v was selected!\n", m.Job.ID.String())
	p = path.Join(jobPath, "image")
	req, err = getJobReq(p, authToken, jobToken)
	if err != nil {
		log.Printf("Error creating request GET %v: %v\n", p, err)
		return
	}
	log.Printf("%v %v\n", req.Method, p)
	resp, err = client.Do(req)
	if err != nil {
		log.Printf("Error %v %v: %v\n", req.Method, p, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("Response header error: %v\n", resp.Status)
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
	defer func() {
		imgDelResp, err := cli.ImageRemove(ctx, m.Job.ID.String(), types.ImageRemoveOptions{
			Force: true,
		})
		if err != nil {
			log.Printf("Error deleting image %v: %v\n", m.Job.ID.String(), err)
			return
		}
		for i := range imgDelResp {
			log.Printf("Deleted: %v", imgDelResp[i].Deleted)
			log.Printf("Untagged: %v", imgDelResp[i].Untagged)
		}
	}()
	check.Err(zResp.Close)
	check.Err(resp.Body.Close)

	user, err := user.Current()
	if err != nil {
		log.Printf("Error getting current user: %v\n", err)
		return
	}
	jobDir := filepath.Join(user.HomeDir, ".emrys", m.Job.ID.String())
	if err = os.MkdirAll(jobDir, 0755); err != nil {
		log.Printf("Error making job dir %v: %v\n", jobDir, err)
		return
	}
	defer check.Err(func() error { return os.RemoveAll(jobDir) })

	p = path.Join(jobPath, "data")
	req, err = getJobReq(p, authToken, jobToken)
	if err != nil {
		log.Printf("Error creating request GET %v: %v\n", p, err)
		return
	}
	log.Printf("%v %v\n", req.Method, p)
	resp, err = client.Do(req)
	if err != nil {
		log.Printf("Error %v %v: %v\n", req.Method, p, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("Response header error: %v\n", resp.Status)
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
		Image: m.Job.ID.String(),
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
		ReadonlyRootfs: true,
		Runtime:        "nvidia",
		SecurityOpt: []string{
			"no-new-privileges",
		},
	}, nil, "")
	if err != nil {
		log.Printf("Error creating container: %v\n", err)
		return
	}

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

	p = path.Join(jobPath, "output", "log")
	req, err = postJobReq(p, authToken, jobToken, out)
	if err != nil {
		log.Printf("Error creating request POST %v: %v\n", p, err)
		return
	}
	log.Printf("%v %v\n", req.Method, p)
	resp, err = client.Do(req)
	if err != nil {
		log.Printf("Error %v %v: %v\n", req.Method, p, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("Response header error: %v\n", resp.Status)
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
	p = path.Join(jobPath, "output", "dir")
	req, err = postJobReq(p, authToken, jobToken, pr)
	if err != nil {
		log.Printf("Error creating request POST %v: %v\n", p, err)
		return
	}
	log.Printf("%v %v\n", req.Method, p)
	resp, err = client.Do(req)
	if err != nil {
		log.Printf("Error %v %v: %v\n", req.Method, p, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("Response header error: %v\n", resp.Status)
		check.Err(resp.Body.Close)
		return
	}

	check.Err(resp.Body.Close)
}
