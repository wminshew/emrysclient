package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/dgrijalva/jwt-go"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/job"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"time"
)

type pollResponse struct {
	Events    []pollEvent `json:"events"`
	Timestamp int64       `json:"timestamp"`
}

// source: https://github.com/jcuga/golongpoll/blob/master/go-client/glpclient/client.go
type pollEvent struct {
	// Timestamp is milliseconds since epoch to match javascripts Date.getTime()
	Timestamp int64  `json:"timestamp"`
	Category  string `json:"category"`
	// Data can be anything that is able to passed to json.Marshal()
	Data json.RawMessage `json:"data"`
}

var (
	busy      = false
	terminate = false
	bidsOut   = 0
	cm        *cryptoMiner
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Begin mining on emrys",
	Long: "Start executing deep learning jobs for money. " +
		"When no jobs are available, or if the asking rates are " +
		"below your minimum, emrysminer will execute ./mining-command",
	Run: func(cmd *cobra.Command, args []string) {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt)
		ctx, cancel := context.WithCancel(context.Background())
		go monitorInterrupts(stop, cancel)
		go monitorGPU(ctx)

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

		cm = &cryptoMiner{
			command: viper.GetString("mining-command"),
		}
		cm.init(ctx)
		defer cm.stop()

		if err := seedDockerdCache(ctx); err != nil {
			log.Printf("Error downloading job image: %v\n", err)
			return
		}

		m := "GET"
		p := path.Join("miner", "connect")
		u.Path = p
		q := u.Query()
		q.Set("timeout", "600")
		buffer := int64(3) // auctions last 3 seconds
		sinceTime := (time.Now().Unix() - buffer) * 1000
		q.Set("since_time", fmt.Sprintf("%d", sinceTime))
		u.RawQuery = q.Encode()

		var req *http.Request
		var resp *http.Response
		var operation backoff.Operation
		pr := pollResponse{}
		for {
			if terminate {
				log.Printf("Mining terminated.\n")
				return
			}

			if err := checkContextCanceled(ctx); err != nil {
				log.Printf("Miner canceled job search: %v\n", err)
				return
			}

			if !busy {
				log.Printf("Pinging emrys for jobs...\n")
				operation = func() error {
					if req, err = http.NewRequest(m, u.String(), nil); err != nil {
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

					if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
						return fmt.Errorf("json decoding response: %v", err)
					}
					return nil
				}
				if err := backoff.RetryNotify(operation,
					backoff.WithContext(backoff.NewExponentialBackOff(), ctx),
					func(err error, t time.Duration) {
						log.Printf("Pinging error: %v\n", err)
						log.Printf("Trying again in %s seconds\n", t.Round(time.Second).String())
					}); err != nil {
					log.Printf("Unable to connect to emrys: %v\n", err)
					os.Exit(1)
				}

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
						bidsOut++
						go bid(ctx, client, u, mID, authToken, msg)
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
				time.Sleep(5 * time.Second)
			}
		}
	},
}

func checkContextCanceled(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
