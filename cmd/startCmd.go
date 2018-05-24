package cmd

import (
	// "compress/zlib"
	// "context"
	// "docker.io/go-docker"
	// "docker.io/go-docker/api/types"
	// "docker.io/go-docker/api/types/container"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"github.com/wminshew/check"
	"github.com/wminshew/emrys/pkg/job"
	"io"
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
						resp, err := post(p, authToken, &body)
						if err != nil {
							log.Printf("Error POST %v: %v\n", p, err)
							return
						}
						defer check.Err(resp.Body.Close)

						if appEnv == "dev" {
							respDump, err := httputil.DumpResponse(resp, true)
							if err != nil {
								log.Println(err)
							}
							log.Println(string(respDump))
						}

						if resp.StatusCode != http.StatusOK {
							log.Printf("Request error: %v\n", resp.Status)
							return
						}

						jobToken := resp.Header.Get("Set-Job-Authorization")
						if jobToken == "" {
							log.Printf("Sorry, your bid (%+v) did not win.\n", b)
							return
						}

						p = path.Join("miner", "job", m.Job.ID.String(), "image")
						resp, err = get(p, authToken, jobToken)
						if err != nil {
							log.Printf("Error GET %v: %v\n", p, err)
							return
						}
						defer check.Err(resp.Body.Close)

						p = path.Join("miner", "job", m.Job.ID.String(), "data")
						resp, err = get(p, authToken, jobToken)
						if err != nil {
							log.Printf("Error GET %v: %v\n", p, err)
							return
						}
						defer check.Err(resp.Body.Close)

						// TODO: use job authToken to .. run?
					}()

					// 	zr, err := zlib.NewReader(r)
					// 	if err != nil {
					// 		log.Printf("Error decompressing image: %v\n", err)
					// 		break
					// 	}
					//
					// 	ctx := context.Background()
					// 	cli, err := docker.NewEnvClient()
					// 	imgResp, err := cli.ImageLoad(ctx, zr, false)
					// 	if err != nil {
					// 		log.Printf("Error loading image: %v\n", err)
					// 		break
					// 	}
					// 	err = zr.Close()
					// 	if err != nil {
					// 		log.Printf("Error closing zlib reader: %v\n", err)
					// 		break
					// 	}
					// 	log.Printf("Image successfully loaded!\n")
					// 	_, err = io.Copy(os.Stdout, imgResp.Body)
					//
					// 	// TODO: do I need to preserve users' file structure?
					// 	// [relative pathing between train.py and path/to/data/]
					// 	// wd, err := os.Getwd()
					// 	// if err != nil {
					// 	// 	log.Printf("Error getting working directory: %v\n", err)
					// 	// 	http.Error(w, err.Error(), http.StatusInternalServerError)
					// 	// 	return
					// 	// }
					// 	// hostDataPath := filepath.Join(wd, userDir, "data")
					// 	// dockerDataPath := filepath.Join(userHome, "data")
					// 	// resp, err := cli.ContainerCreate(ctx, &container.Config{
					// 	// 	// Image: j.ID.String(),
					// 	// 	Image: string(id),
					// 	// 	Tty:   true,
					// 	// }, &container.HostConfig{
					// 	// 	AutoRemove: true,
					// 	// 	// Binds: []string{
					// 	// 	// 	fmt.Sprintf("%s:%s:ro", hostDataPath, dockerDataPath),
					// 	// 	// },
					// 	// 	CapDrop: []string{
					// 	// 		"ALL",
					// 	// 	},
					// 	// 	// TODO: mount a rw drive and use readonlyrootfs
					// 	// 	// ReadonlyRootfs: true,
					// 	// 	Runtime: "nvidia",
					// 	// 	SecurityOpt: []string{
					// 	// 		"no-new-privileges",
					// 	// 	},
					// 	// }, nil, "")
					// 	// if err != nil {
					// 	// 	log.Printf("Error creating container: %v\n", err)
					// 	// 	return
					// 	// }
					// 	//
					// 	// if err := cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
					// 	// 	log.Printf("Error starting container: %v\n", err)
					// 	// 	return
					// 	// }
					// 	//
					// 	// out, err := cli.ContainerLogs(ctx, resp.ID, types.ContainerLogsOptions{
					// 	// 	Follow:     true,
					// 	// 	ShowStdout: true,
					// 	// })
					// 	// if err != nil {
					// 	// 	log.Printf("Error logging container: %v\n", err)
					// 	// 	return
					// 	// }
					// 	//
					// 	// // tee := io.TeeReader(out, fw)
					// 	// // _, err = io.Copy(os.Stdout, tee)
					// 	// // if err != nil && err != io.EOF {
					// 	// // 	log.Printf("Error copying to stdout: %v\n", err)
					// 	// // 	return
					// 	// // }
					// 	// scanner := bufio.NewScanner(out)
					// 	// for scanner.Scan() {
					// 	// 	log.Println(scanner.Text())
					// 	// }
					// 	// // response <- []byte(out)
					// 	//
					// 	// err = out.Close()
					// 	// if err != nil {
					// 	// 	log.Printf("Error closing container log: %v\n", err)
					// 	// 	break
					// 	// }
					//
					// 	err = imgResp.Body.Close()
					// 	if err != nil {
					// 		log.Printf("Error closing zlib reader: %v\n", err)
					// 		break
					// 	}
					// case "Data\n":
					// 	log.Printf("Incoming data\n")
					// default:
					// 	_, err = io.Copy(os.Stdout, &b)
					// 	if err != nil && err != io.EOF {
					// 		log.Printf("Error copying websocket.TextMessage to os.Stdout: %v\n", err)
					// 		break
					// 	}
					// }

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

func post(path, authToken string, body io.Reader) (*http.Response, error) {
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

	client := resolveClient()
	log.Printf("POST %v\n", path)
	return client.Do(req)
}

func get(path, authToken, jobToken string) (*http.Response, error) {
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
	req.Header.Set("Job-Authorization", fmt.Sprintf("%v", jobToken))

	client := resolveClient()
	log.Printf("GET %v\n", path)
	return client.Do(req)
}
