package cmd

import (
	"context"
	"docker.io/go-docker"
	"docker.io/go-docker/api/types"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/dgrijalva/jwt-go"
	"github.com/fsnotify/fsnotify"
	"github.com/satori/go.uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/job"
	"github.com/wminshew/gonvml"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strconv"
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
	terminate     = false
	jobsInProcess = 0
	bidsOut       = 0
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Begin mining on emrys",
	Long: "Start training deep learning models for money. " +
		"When no jobs are available, or if the asking rates are " +
		"below your minimum, emrysminer will execute ./mining-command",
	Run: func(cmd *cobra.Command, args []string) {
		if os.Geteuid() != 0 {
			log.Printf("Insufficient privileges. Are you root?\n")
			return
		}

		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt)
		ctx, cancel := context.WithCancel(context.Background())
		go monitorInterrupts(stop, cancel)

		authToken, err := getToken()
		if err != nil {
			log.Printf("Error getting authToken: %v", err)
			// TODO: test to see if you end up with stray processes on returning here; may have to close(stop) or something similar
			return
		}
		claims := &jwt.StandardClaims{}
		if _, _, err := new(jwt.Parser).ParseUnverified(authToken, claims); err != nil {
			log.Printf("Error parsing authToken %v: %v\n", authToken, err)
			return
		}
		if err := claims.Valid(); err != nil {
			log.Printf("Error invalid authToken claims: %v", err)
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

		dClient, err := docker.NewEnvClient()
		if err != nil {
			log.Printf("Error creating docker client: %v", err)
			panic(err)
		}
		defer check.Err(dClient.Close)

		if err := checkVersion(ctx, client, u); err != nil {
			log.Printf("Version error: %v", err)
			return
		}

		viper.SetConfigName(viper.GetString("config"))
		viper.AddConfigPath(".")
		if err := viper.ReadInConfig(); err != nil {
			log.Printf("Error reading config file: %v", err)
			return
		}

		if err := gonvml.Initialize(); err != nil {
			log.Printf("Error initializing gonvml: %v. Please make sure NVML is in the shared library search path.", err)
			panic(err)
		}
		defer check.Err(gonvml.Shutdown)

		driverVersion, err := gonvml.SystemDriverVersion()
		if err != nil {
			log.Printf("Error finding nvidia driver: %v", err)
			return
		}
		log.Printf("Nvidia driver: %v\n", driverVersion)

		devices := []uint{}
		devicesStr := viper.GetStringSlice("devices")
		if len(devicesStr) == 0 {
			// no flag provided, grab all detected devices
			numDevices, err := gonvml.DeviceCount()
			if err != nil {
				log.Printf("Error counting nvidia devices: %v", err)
				return
			}
			for i := 0; i < int(numDevices); i++ {
				devices = append(devices, uint(i))
			}
		} else {
			// flag provided, convert to uints
			for _, s := range devicesStr {
				u, err := strconv.ParseUint(s, 10, 64)
				if err != nil {
					log.Printf("Invalid devices entry %s: %v", s, err)
					return
				}
				devices = append(devices, uint(u))
			}
		}

		bidRatesStr := viper.GetStringSlice("bid-rates")
		if len(bidRatesStr) != 1 && len(bidRatesStr) != len(devices) {
			log.Printf("Mismatch between number of devices (%d) and bid-rates (%d). Either set a single bid rate for all devices, or one for each device.\n",
				len(devices), len(bidRatesStr))
			return
		}
		workers := []worker{}
		for i, d := range devices {
			dev, err := gonvml.DeviceHandleByIndex(d)
			if err != nil {
				log.Printf("Device %d: DeviceHandleByIndex() error: %v", d, err)
				return
			}
			dUUIDStr, err := dev.UUID()
			if err != nil {
				log.Printf("Device %d: UUID() error: %v", d, err)
				return
			}
			dUUID, err := uuid.FromString(dUUIDStr[4:]) // strip off "GPU-" prepend
			if err != nil {
				log.Printf("Device %d: error converting device uuid to uuid.UUID: %v", d, err)
				return
			}
			var brStr string
			if len(bidRatesStr) == 1 {
				brStr = bidRatesStr[0]
			} else {
				brStr = bidRatesStr[i]
			}
			br, err := strconv.ParseFloat(brStr, 64)
			if err != nil {
				log.Printf("Invalid bid-rate entry %s: %v", brStr, err)
				return
			}

			cm := &cryptoMiner{
				command: viper.GetString("mining-command"),
				device:  d,
			}
			w := worker{
				device:  d,
				uuid:    dUUID,
				busy:    false,
				jID:     "",
				bidRate: br,
				miner:   cm,
			}

			go w.monitorGPU(ctx, client, u, authToken)
			w.miner.init(ctx)
			defer w.miner.stop()

			workers = append(workers, w)
		}

		// TODO: make sure .. things are updated properly on change? run some tests to get working
		viper.WatchConfig()
		viper.OnConfigChange(func(e fsnotify.Event) {
			log.Printf("Config file changed: %v %v\n", e.Op, e.Name)
			// TODO: update cryptominer, if necessary
			// TODO: update worker, if necessary
		})

		dockerAuthConfig := types.AuthConfig{
			RegistryToken: authToken,
		}
		dockerAuthJSON, err := json.Marshal(dockerAuthConfig)
		if err != nil {
			log.Printf("Error marshaling docker auth config: %v", err)
			return
		}
		dockerAuthStr := base64.URLEncoding.EncodeToString(dockerAuthJSON)

		if err := seedDockerdCache(ctx, dClient, dockerAuthStr); err != nil {
			log.Printf("Error seeding docker cache: %v", err)
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
		log.Printf("Connecting to emrys for jobs...\n")
		for {
			if terminate {
				log.Printf("Mining job search canceled.\n")
				return
			}
			if err := checkVersion(ctx, client, u); err != nil {
				log.Printf("Version error: %v", err)
				return
			}

			operation = func() error {
				if req, err = http.NewRequest(m, u.String(), nil); err != nil {
					return fmt.Errorf("creating request %v %v: %v", m, u.Path, err)
				}
				req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
				req = req.WithContext(ctx)

				resp, err = client.Do(req)
				if err != nil {
					return err
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
					log.Printf("Connect error: %v", err)
					log.Printf("Trying again in %s seconds\n", t.Round(time.Second).String())
				}); err != nil {
				log.Printf("Connect error: %v", err)
				os.Exit(1)
			}

			if err := checkContextCanceled(ctx); err != nil {
				log.Printf("Miner canceled job search: %v", err)
				return
			}

			if len(pr.Events) > 0 {
				log.Println(len(pr.Events), "job(s) up for auction")
				for _, event := range pr.Events {
					sinceTime = event.Timestamp
					msg := &job.Message{}
					if err := json.Unmarshal(event.Data, msg); err != nil {
						log.Printf("Error unmarshaling json message: %v", err)
						log.Println("json message: ", string(event.Data))
						continue
					}
					if msg.Job == nil {
						continue
					}
					for _, worker := range workers {
						w := worker
						if !w.busy {
							go w.bid(ctx, dClient, client, u, mID, authToken, dockerAuthStr, msg)
						}
					}
				}
			} else {
				if pr.Timestamp > sinceTime {
					sinceTime = pr.Timestamp
				}
			}

			q = u.Query()
			q.Set("since_time", fmt.Sprintf("%d", sinceTime))
			u.RawQuery = q.Encode()
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
