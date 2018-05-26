package cmd

import (
	// "bufio"
	"bytes"
	"compress/zlib"
	"context"
	"docker.io/go-docker"
	"docker.io/go-docker/api/types"
	"docker.io/go-docker/api/types/container"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/mholt/archiver"
	"github.com/spf13/cobra"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/job"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
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

		conn, _, err := dialWebsocket(authToken)
		if err != nil {
			log.Printf("Error dialing websocket: %v\n", err)
			if err == websocket.ErrBadHandshake {
				log.Printf("Are you logged in? Your authToken may have expired.\n")
			}
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
					log.Printf("Job: %+v\n", m.Job)

					go func() {
						client := resolveClient()
						b := &job.Bid{
							MinRate: 0.2,
						}
						log.Printf("Sending bid: %+v\n", b)

						var body bytes.Buffer
						p := path.Join("miner", "job", m.Job.ID.String(), "bid")
						err = json.NewEncoder(&body).Encode(b)
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

						if appEnv == "dev" {
							respDump, err := httputil.DumpResponse(resp, true)
							if err != nil {
								log.Println(err)
							}
							log.Println(string(respDump))
						}

						if resp.StatusCode != http.StatusOK {
							log.Printf("Response header error: %v\n", resp.Status)
							check.Err(resp.Body.Close)
							return
						}

						jobToken := resp.Header.Get("Set-Job-Authorization")
						if jobToken == "" {
							log.Printf("Sorry, your bid (%+v) did not win.\n", b)
							check.Err(resp.Body.Close)
							return
						}

						_, _ = io.Copy(ioutil.Discard, resp.Body)
						check.Err(resp.Body.Close)

						p = path.Join("miner", "job", m.Job.ID.String(), "image")
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
						err = zResp.Close()
						if err != nil {
							log.Printf("Error closing zlib img reader: %v\n", err)
							check.Err(resp.Body.Close)
							return
						}

						_, _ = io.Copy(ioutil.Discard, resp.Body)
						check.Err(resp.Body.Close)

						p = path.Join("miner", "job", m.Job.ID.String(), "data")
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

						wd, err := os.Getwd()
						if err != nil {
							log.Printf("Error getting working directory: %v\n", err)
							check.Err(resp.Body.Close)
							return
						}
						hostDataDir := path.Join(wd, ".emrysminer", "temp-job-data")
						hostDataPath := path.Join(hostDataDir, "data")

						if err = os.MkdirAll(hostDataPath, 0755); err != nil {
							log.Printf("Error making data dir %v: %v\n", hostDataPath, err)
							check.Err(resp.Body.Close)
							return
						}
						if err = archiver.TarGz.Read(resp.Body, hostDataDir); err != nil {
							log.Printf("Error unpacking .tar.gz into data dir %v: %v\n", hostDataPath, err)
							check.Err(resp.Body.Close)
							return
						}
						check.Err(resp.Body.Close)
						defer check.Err(func() error { return os.RemoveAll(hostDataDir) })
						userHome := "/home/user"
						dockerDataPath := path.Join(userHome, "data")
						c, err := cli.ContainerCreate(ctx, &container.Config{
							Image: m.Job.ID.String(),
							Tty:   true,
						}, &container.HostConfig{
							AutoRemove: true,
							Binds: []string{
								fmt.Sprintf("%s:%s:ro", hostDataPath, dockerDataPath),
							},
							CapDrop: []string{
								"ALL",
							},
							// TODO: mount a rw drive and use readonlyrootfs
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

						if err := cli.ContainerStart(ctx, c.ID, types.ContainerStartOptions{}); err != nil {
							log.Printf("Error starting container: %v\n", err)
							return
						}

						out, err := cli.ContainerLogs(ctx, c.ID, types.ContainerLogsOptions{
							Follow:     true,
							ShowStdout: true,
						})
						if err != nil {
							log.Printf("Error logging container: %v\n", err)
							return
						}

						p = path.Join("miner", "job", m.Job.ID.String(), "output", "log")
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
					}()

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

func dialWebsocket(t string) (*websocket.Conn, *http.Response, error) {
	h := resolveHost()
	u := url.URL{
		Scheme: "wss",
		Host:   h,
		Path:   "/miner/connect",
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
