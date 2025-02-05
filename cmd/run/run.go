package run

import (
	"context"
	"fmt"
	"github.com/dgrijalva/jwt-go"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wminshew/emrys/pkg/check"
	specs "github.com/wminshew/emrys/pkg/job"
	"github.com/wminshew/emrysclient/cmd/version"
	"github.com/wminshew/emrysclient/pkg/job"
	"github.com/wminshew/emrysclient/pkg/token"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const (
	buffer = 1 * time.Second
)

func init() {
	Cmd.Flags().StringP("config", "c", ".emrys", "Path to config file (don't include extension). Defaults to .emrys")
	Cmd.Flags().StringP("project", "p", "", "User project (required)")
	Cmd.Flags().StringP("conda-env", "e", "", "Path to conda environment yaml")
	Cmd.Flags().StringP("pip-reqs", "r", "", "Path to pip requirements file")
	Cmd.Flags().StringP("main", "m", "", "Path to main execution file (required)")
	Cmd.Flags().StringP("data", "d", "", "Path to the data directory")
	Cmd.Flags().StringP("output", "o", "", "Path to save the output directory (required)")
	Cmd.Flags().Float64("rate", 0, "Maximum $ / hr willing to pay for job")
	Cmd.Flags().String("gpu", "k80", "Minimum acceptable gpu for job. Defaults to k80")
	Cmd.Flags().String("ram", "8gb", "Minimum acceptable gb of available ram for job. Defaults to 8gb")
	Cmd.Flags().String("disk", "25gb", "Minimum acceptable gb of disk space for job. Defaults to 25gb")
	Cmd.Flags().String("pcie", "8x", "Minimum acceptable gpu pci-e for job. Defaults to 8x")
	Cmd.Flags().SortFlags = false
}

// Cmd exports run subcommand to root
var Cmd = &cobra.Command{
	Use:   "run",
	Short: "Dispatch a deep learning job",
	Long: "Syncs the appropriate execution files & data " +
		"with the central server, then locates the cheapest " +
		"spare GPU cycles on the internet to execute your job" +
		"\n\nReport bugs to support@emrys.io or with the feedback subcommand" +
		"\nIf you have any questions, please visit our forum https://forum.emrys.io " +
		"or slack channel https://emrysio.slack.com",
	PreRun: func(cmd *cobra.Command, args []string) {
		if err := func() error {
			if err := viper.BindPFlag("config", cmd.Flags().Lookup("config")); err != nil {
				return err
			}
			if err := viper.BindPFlag("user.project", cmd.Flags().Lookup("project")); err != nil {
				return err
			}
			if err := viper.BindPFlag("user.conda-env", cmd.Flags().Lookup("conda-env")); err != nil {
				return err
			}
			if err := viper.BindPFlag("user.pip-reqs", cmd.Flags().Lookup("pip-reqs")); err != nil {
				return err
			}
			if err := viper.BindPFlag("user.main", cmd.Flags().Lookup("main")); err != nil {
				return err
			}
			if err := viper.BindPFlag("user.data", cmd.Flags().Lookup("data")); err != nil {
				return err
			}
			if err := viper.BindPFlag("user.output", cmd.Flags().Lookup("output")); err != nil {
				return err
			}
			if err := viper.BindPFlag("user.rate", cmd.Flags().Lookup("rate")); err != nil {
				return err
			}
			if err := viper.BindPFlag("user.gpu", cmd.Flags().Lookup("gpu")); err != nil {
				return err
			}
			if err := viper.BindPFlag("user.ram", cmd.Flags().Lookup("ram")); err != nil {
				return err
			}
			if err := viper.BindPFlag("user.disk", cmd.Flags().Lookup("disk")); err != nil {
				return err
			}
			if err := viper.BindPFlag("user.pcie", cmd.Flags().Lookup("pcie")); err != nil {
				return err
			}
			return nil
		}(); err != nil {
			log.Printf("Run: error binding pflag: %v", err)
			panic(err)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		authToken, err := token.Get()
		if err != nil {
			log.Printf("Run: error retrieving authToken: %v", err)
			return
		}
		claims := &jwt.StandardClaims{}
		if _, _, err := new(jwt.Parser).ParseUnverified(authToken, claims); err != nil {
			log.Printf("Run: error parsing authToken: %v", err)
			return
		}
		if err := claims.Valid(); err != nil {
			log.Printf("Run: invalid authToken: %v", err)
			log.Printf("Run: please login again.\n")
			return
		}
		exp := claims.ExpiresAt
		refreshAt := time.Unix(exp, 0).Add(token.RefreshBuffer)
		if refreshAt.Before(time.Now()) {
			log.Printf("Run: token too close to expiration, please login again.")
			return
		}

		var uid, gid int
		if os.Geteuid() == 0 && os.Getenv("SUDO_USER") != "" {
			sudoUser, err := user.Lookup(os.Getenv("SUDO_USER"))
			if err != nil {
				log.Printf("Run: error getting current sudo user: %v", err)
				return
			}
			if uid, err = strconv.Atoi(sudoUser.Uid); err != nil {
				log.Printf("Run: error converting uid to int: %v", err)
				return
			}
			if gid, err = strconv.Atoi(sudoUser.Gid); err != nil {
				log.Printf("Run: error converting gid to int: %v", err)
				return
			}
		}

		viper.SetConfigName(viper.GetString("config"))
		viper.AddConfigPath(".")
		viper.AddConfigPath("$HOME/.config/emrys")
		viper.AddConfigPath("$HOME")
		if err := viper.ReadInConfig(); err != nil {
			log.Printf("Run: error reading config file: %v", err)
			return
		}

		client := &http.Client{}
		s := "https"
		h := "api.emrys.io"
		u := url.URL{
			Scheme: s,
			Host:   h,
		}

		j := &job.Job{
			Client:    client,
			AuthToken: authToken,
			Notebook:  false,
			Project:   viper.GetString("user.project"),
			CondaEnv:  viper.GetString("user.conda-env"),
			PipReqs:   viper.GetString("user.pip-reqs"),
			Main:      viper.GetString("user.main"),
			Data:      viper.GetString("user.data"),
			Output:    viper.GetString("user.output"),
			GPURaw:    viper.GetString("user.gpu"),
			RAMStr:    viper.GetString("user.ram"),
			DiskStr:   viper.GetString("user.disk"),
			PCIEStr:   viper.GetString("user.pcie"),
			Specs: &specs.Specs{
				Rate: viper.GetFloat64("user.rate"),
			},
		}
		if err := j.ValidateAndTransform(); err != nil {
			log.Printf("Run: invalid requirements: %v", err)
			return
		}

		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt)
		ctx, cancel := context.WithCancel(context.Background())
		defer func() {
			select {
			case <-ctx.Done():
				return
			default:
				cancel()
			}
		}()
		jobCanceled := false
		auctionComplete := false
		go func() {
			select {
			case <-stop:
				jobCanceled = true
				log.Printf("Cancellation request received: please wait for job to successfully cancel\n")
				log.Printf("Warning: failure to successfully cancel job may result in undesirable charges\n")
				// j.cancel returns when job successfully canceled
				if err := j.Cancel(u); err != nil {
					log.Printf("Run: error canceling: %v", err)
					return
				}
				if !auctionComplete {
					cancel()
				}
			case <-ctx.Done():
				return
			}
		}()

		if err := version.CheckRun(ctx, client, u); err != nil {
			log.Printf("Run: version error: %v", err)
			log.Printf("Please execute emrys update")
			return
		}

		if err := j.Send(ctx, u); err != nil {
			log.Printf("Run: error sending requirements: %v", err)
			return
		}

		go func() {
			for {
				if err := token.Monitor(ctx, client, u, &j.AuthToken, refreshAt); err != nil {
					log.Printf("Run: token: refresh error: %v", err)
				}
				select {
				case <-ctx.Done():
					return
				default:
				}
			}
		}()

		if err := check.ContextCanceled(ctx); err != nil {
			return
		}
		errCh := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go j.BuildImage(ctx, &wg, errCh, u)
		go j.SyncData(ctx, &wg, errCh, u)
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-ctx.Done():
			return
		case <-errCh:
			if err := j.Cancel(u); err != nil {
				log.Printf("Run: error canceling: %v", err)
				return
			}
			return
		case <-done:
		}

		if err := j.RunAuction(ctx, u); err != nil {
			if err := j.Cancel(u); err != nil {
				log.Printf("Run: error canceling: %v", err)
				return
			}
			return // already logged
		}
		auctionComplete = true

		if jobCanceled {
			return
		}
		outputDir := filepath.Join(j.Output, j.ID)
		if err = os.MkdirAll(outputDir, 0755); err != nil {
			log.Printf("Output data: error making output dir %v: %v", outputDir, err)
			return
		}

		log.Printf("Executing job %s...\n", j.ID)
		if err := j.StreamOutputLog(ctx, u); err != nil {
			log.Printf("Output log: error: %v", err)
			return
		}
		// TODO: replace w/ longpoll checking when miner has started uploading output data
		time.Sleep(buffer)
		if err := j.DownloadOutputData(ctx, u); err != nil {
			log.Printf("Output data: error: %v", err)
			return
		}

		if os.Geteuid() == 0 && os.Getenv("SUDO_USER") != "" {
			if err = os.Chown(j.Output, uid, gid); err != nil {
				log.Printf("Run: error changing ownership: %v", err)
			}

			if err := filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if err = os.Chown(path, uid, gid); err != nil {
					return fmt.Errorf("changing ownership: %v", err)
				}

				return nil
			}); err != nil {
				log.Printf("Run: error walking output directory: %v", err)
			}
		}

		if !jobCanceled {
			log.Printf("Complete!\n")
		}
	},
}
