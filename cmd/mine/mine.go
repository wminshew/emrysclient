package mine

import (
	"context"
	"docker.io/go-docker"
	"docker.io/go-docker/api/types"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/dgrijalva/jwt-go"
	"github.com/dustin/go-humanize"
	"github.com/fsnotify/fsnotify"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/mem"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/job"
	"github.com/wminshew/emrysclient/cmd/version"
	"github.com/wminshew/emrysclient/pkg/poll"
	"github.com/wminshew/emrysclient/pkg/token"
	"github.com/wminshew/emrysclient/pkg/worker"
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
	"strings"
	"time"
)

var (
	terminate     = false
	jobsInProcess = 0
	bidsOut       = 0
)

const (
	maxRetries = 10
	gpuPeriod  = 10 * time.Second
	meanPeriod = 30 * time.Second
	maxPeriod  = 90 * time.Second
)

func init() {
	Cmd.Flags().StringP("config", "c", ".emrys", "Path to config file (don't include extension)")
	Cmd.Flags().StringSliceP("devices", "d", []string{}, "Cuda devices to mine with on emrys. If blank, program will mine with all detected devices.")
	Cmd.Flags().StringSliceP("bid-rates", "b", []string{}, "Per device bid rates ($/hr) for mining jobs (required; may set 1 value for all devices, or 1 value per device)")
	Cmd.Flags().StringSlice("ram", []string{"8gb"}, "Per device RAM allocation for mining jobs (defaults to 8gb; may set 1 value for all devices, or 1 value per device)")
	Cmd.Flags().StringSlice("disk", []string{"25gb"}, "Per device disk allocation for mining jobs (defaults to 25gb; may set 1 value for all devices, or 1 value per device)")
	Cmd.Flags().StringP("mining-command", "m", "", "Mining command to execute between emrys jobs. Must use $DEVICE flag so emrys can toggle mining-per-device correctly between jobs.")
	Cmd.Flags().SortFlags = false
	if err := func() error {
		if err := viper.BindPFlag("config", Cmd.Flags().Lookup("config")); err != nil {
			return err
		}
		if err := viper.BindPFlag("miner.devices", Cmd.Flags().Lookup("devices")); err != nil {
			return err
		}
		if err := viper.BindPFlag("miner.bid-rates", Cmd.Flags().Lookup("bid-rates")); err != nil {
			return err
		}
		if err := viper.BindPFlag("miner.ram", Cmd.Flags().Lookup("ram")); err != nil {
			return err
		}
		if err := viper.BindPFlag("miner.disk", Cmd.Flags().Lookup("disk")); err != nil {
			return err
		}
		if err := viper.BindPFlag("miner.mining-command", Cmd.Flags().Lookup("mining-command")); err != nil {
			return err
		}
		return nil
	}(); err != nil {
		log.Printf("Mine: error binding pflag: %v", err)
		panic(err)
	}
}

// Cmd exports mine subcommand to root
var Cmd = &cobra.Command{
	Use:   "mine",
	Short: "Begin mining on emrys",
	Long: "Earn money by training deep learning models for emrys. " +
		"When no jobs are available, or if the asking rates are " +
		"below your bid-rate, emrys will execute ./mining-command" +
		"\n\nReport bugs to support@emrys.io or with the feedback subcommand" +
		"\nIf you have any questions, please visit our forum https://forum.emrys.io " +
		"or slack channel https://emrysio.slack.com",
	Run: func(cmd *cobra.Command, args []string) {
		if os.Geteuid() != 0 {
			log.Printf("Insufficient privileges. Are you root?\n")
			return
		}

		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go monitorInterrupts(ctx, stop, cancel)

		authToken, err := token.Get()
		if err != nil {
			log.Printf("Mine: error getting authToken: %v", err)
			return
		}
		claims := &jwt.StandardClaims{}
		if _, _, err := new(jwt.Parser).ParseUnverified(authToken, claims); err != nil {
			log.Printf("Mine: error parsing authToken %v: %v\n", authToken, err)
			return
		}
		if err := claims.Valid(); err != nil {
			log.Printf("Mine: invalid authToken: %v", err)
			log.Printf("Please login again.\n")
			return
		}
		mID := claims.Subject
		exp := claims.ExpiresAt
		refreshAt := time.Unix(exp, 0).Add(token.RefreshBuffer)
		if refreshAt.Before(time.Now()) {
			log.Printf("Mine: token too close to expiration, please login again.")
			return
		}

		tr := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   60 * time.Second,
				KeepAlive: 60 * time.Second,
				DualStack: true,
			}).DialContext,
			MaxIdleConns:          50,
			IdleConnTimeout:       60 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DisableCompression:    true,
		}
		client := &http.Client{Transport: tr}
		s := "https"
		h := "api.emrys.io"
		u := url.URL{
			Scheme: s,
			Host:   h,
		}

		go func() {
			for {
				if err := token.Monitor(ctx, client, u, &authToken, refreshAt); err != nil {
					log.Printf("Token: refresh error: %v", err)
				}
				select {
				case <-ctx.Done():
					return
				default:
				}
			}
		}()

		dClient, err := docker.NewEnvClient()
		if err != nil {
			log.Printf("Mine: error creating docker client: %v", err)
			return
		}
		defer check.Err(dClient.Close)

		if err := version.CheckMine(ctx, client, u); err != nil {
			log.Printf("Version error: %v", err)
			log.Printf("Please execute emrys update")
			return
		}

		viper.SetConfigName(viper.GetString("config"))
		viper.AddConfigPath("$HOME")
		viper.AddConfigPath("$HOME/.config/emrys")
		viper.AddConfigPath(".")
		if err := viper.ReadInConfig(); err != nil {
			log.Printf("Mine: error reading config file: %v", err)
			return
		}

		miningCmdStr := viper.GetString("miner.mining-command")
		if miningCmdStr != "" && !strings.Contains(miningCmdStr, "$DEVICE") {
			log.Printf("Mine: error: if mining-command is set, it must include $DEVICE")
			return
		}

		if err := gonvml.Initialize(); err != nil {
			log.Printf("Mine: error initializing gonvml: %v. Please make sure NVML is in the shared library search path.", err)
			return
		}
		defer check.Err(gonvml.Shutdown)

		driverVersion, err := gonvml.SystemDriverVersion()
		if err != nil {
			log.Printf("Mine: error finding nvidia driver: %v", err)
			return
		}
		log.Printf("Nvidia driver: %v\n", driverVersion)

		devices := []uint{}
		devicesStr := viper.GetStringSlice("miner.devices")
		if len(devicesStr) == 0 {
			// no flag provided, grab all detected devices
			numDevices, err := gonvml.DeviceCount()
			if err != nil {
				log.Printf("Mine: error counting nvidia devices: %v", err)
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

		bidRatesStr := viper.GetStringSlice("miner.bid-rates")
		if len(bidRatesStr) != 1 && len(bidRatesStr) != len(devices) {
			log.Printf("Mismatch between number of devices (%d) and bid-rates (%d). Either set a single bid rate for all devices, or one for each device.\n",
				len(devices), len(bidRatesStr))
			return
		}

		ramStrs := viper.GetStringSlice("miner.ram")
		if len(ramStrs) != 1 && len(ramStrs) != len(devices) {
			log.Printf("Mismatch between number of devices (%d) and ram allocations (%d). Either set a single ram allocation for each device, or one for each device.\n",
				len(devices), len(ramStrs))
			return
		}

		diskStrs := viper.GetStringSlice("miner.disk")
		if len(diskStrs) != 1 && len(diskStrs) != len(devices) {
			log.Printf("Mismatch between number of devices (%d) and disk allocations (%d). Either set a single disk allocation for each device, or one for each device.\n",
				len(devices), len(diskStrs))
			return
		}

		workers := []*worker.Worker{}
		nextOpenPort := 8889
		var totalRAM, totalDisk uint64
		for i, d := range devices {
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

			var ramStr string
			if len(ramStrs) == 1 {
				ramStr = ramStrs[0]
			} else {
				ramStr = ramStrs[i]
			}
			ram, err := humanize.ParseBytes(ramStr)
			if err != nil {
				log.Printf("Invaid ram entry %s: %v", ramStr, err)
				return
			}

			var diskStr string
			if len(diskStrs) == 1 {
				diskStr = diskStrs[0]
			} else {
				diskStr = diskStrs[i]
			}
			disk, err := humanize.ParseBytes(diskStr)
			if err != nil {
				log.Printf("Invaid disk entry %s: %v", diskStr, err)
				return
			}

			cm := &worker.CryptoMiner{
				Command: miningCmdStr,
				Device:  d,
			}
			w := &worker.Worker{
				MinerID:       mID,
				Client:        client,
				Docker:        dClient,
				AuthToken:     &authToken,
				BidsOut:       &bidsOut,
				JobsInProcess: &jobsInProcess,
				Device:        d,
				Snapshot:      &job.DeviceSnapshot{},
				Busy:          false,
				JobID:         "",
				BidRate:       br,
				RAM:           ram,
				Disk:          disk,
				Miner:         cm,
				Port:          fmt.Sprintf("%d", nextOpenPort),
			}
			nextOpenPort++
			totalRAM += ram
			totalDisk += disk

			w.Miner.Init(ctx)
			defer w.Miner.Stop()

			if err := w.InitGPUMonitoring(); err != nil {
				log.Printf("Mine: error initializing gpu monitoring: %v", err)
				return
			}

			go w.UserGPULog(ctx, gpuPeriod)

			workers = append(workers, w)
		}

		memStats, err := mem.VirtualMemoryWithContext(ctx)
		if err != nil {
			log.Printf("Mine: error getting memory stats: %v", err)
			return
		} else if totalRAM > memStats.Available {
			log.Printf("Mine: insufficient available memory (requested for bidding: %s "+
				"> system memory available %s)", humanize.Bytes(totalRAM), humanize.Bytes(memStats.Available))
			return
		}

		diskUsage, err := disk.UsageWithContext(ctx, "/")
		if err != nil {
			log.Printf("Mine: error getting disk usage: %v", err)
			return
		} else if totalDisk > diskUsage.Free {
			log.Printf("Mine: insufficient available disk space (requested for bidding: %s "+
				"> system disk space available %s)", humanize.Bytes(totalDisk), humanize.Bytes(diskUsage.Free))
			return
		}

		go MonitorMiner(ctx, client, dClient, &authToken, workers, cancel, u)

		viper.WatchConfig()
		viper.OnConfigChange(func(e fsnotify.Event) {
			log.Printf("Config file changed: %v %v\n", e.Op, e.Name)
			// TODO: update cryptominer command
			// TODO: update worker bid-rate
			// TODO: check if system has sufficient ram / disk for new totalRAM/totalDisk? will be tricky
		})

		dockerAuthConfig := types.AuthConfig{
			RegistryToken: authToken,
		}
		dockerAuthJSON, err := json.Marshal(dockerAuthConfig)
		if err != nil {
			log.Printf("Mine: error marshaling docker auth config: %v", err)
			return
		}
		dockerAuthStr := base64.URLEncoding.EncodeToString(dockerAuthJSON)

		if err := seedDockerdCache(ctx, dClient, dockerAuthStr); err != nil {
			log.Printf("Mine: error seeding docker cache: %v", err)
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

		log.Printf("Connecting to emrys for jobs...\n")
		for {
			pr := poll.Response{}
			if terminate {
				log.Printf("Mining job search canceled.\n")
				return
			}
			if err := version.CheckMine(ctx, client, u); err != nil {
				log.Printf("Version error: %v", err)
				return
			}

			operation := func() error {
				req, err := http.NewRequest(m, u.String(), nil)
				if err != nil {
					return fmt.Errorf("creating request %v %v: %v", m, u.Path, err)
				}
				req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
				req = req.WithContext(ctx)

				resp, err := client.Do(req)
				if err != nil {
					return err
				}
				defer check.Err(resp.Body.Close)

				if resp.StatusCode == http.StatusBadGateway {
					return fmt.Errorf("server: temporary error")
				} else if resp.StatusCode >= 300 {
					b, _ := ioutil.ReadAll(resp.Body)
					return backoff.Permanent(fmt.Errorf("server: %v", string(b)))
				}

				if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
					return fmt.Errorf("decoding response: %v", err)
				}

				return nil
			}
			if err := backoff.RetryNotify(operation,
				backoff.WithContext(backoff.NewExponentialBackOff(), ctx),
				func(err error, t time.Duration) {
					log.Printf("Connect error: %v", err)
					log.Printf("Retrying in %s seconds\n", t.Round(time.Second).String())
				}); err != nil {
				log.Printf("Connect error: %v", err)
				return
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
						log.Printf("Mine: error unmarshaling json message: %v", err)
						continue
					}
					if msg.Job == nil {
						continue
					}
					for _, worker := range workers {
						w := worker
						if !w.Busy {
							go func() {
								if err := w.Bid(ctx, u, msg); err != nil {
									log.Printf("Mine: bid: %v", err)
								}
							}()
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
