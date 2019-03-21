package run

import (
	"context"
	"fmt"
	"github.com/dgrijalva/jwt-go"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wminshew/emrys/pkg/job"
	"github.com/wminshew/emrysclient/cmd/version"
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
	maxBackoffRetries = 5
	buffer            = 1 * time.Second
	post              = "POST"
	get               = "GET"
)

func init() {
	Cmd.Flags().String("config", ".emrys", "Path to config file (don't include extension). Defaults to .emrys")
	Cmd.Flags().String("project", "", "User project (required)")
	Cmd.Flags().String("requirements", "", "Path to requirements file (required)")
	Cmd.Flags().String("main", "", "Path to main execution file (required)")
	Cmd.Flags().String("data", "", "Path to the data directory")
	Cmd.Flags().String("output", "", "Path to save the output directory (required)")
	Cmd.Flags().Float64("rate", 0, "Maximum $ / hr willing to pay for job")
	Cmd.Flags().String("gpu", "k80", "Minimum acceptable gpu for job. Defaults to k80")
	Cmd.Flags().String("ram", "8gb", "Minimum acceptable gb of available ram for job. Defaults to 8gb")
	Cmd.Flags().String("disk", "25gb", "Minimum acceptable gb of disk space for job. Defaults to 25gb")
	Cmd.Flags().String("pcie", "8x", "Minimum acceptable gpu pci-e for job. Defaults to 8x")
	Cmd.Flags().SortFlags = false
	if err := func() error {
		if err := viper.BindPFlag("config", Cmd.Flags().Lookup("config")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.project", Cmd.Flags().Lookup("project")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.requirements", Cmd.Flags().Lookup("requirements")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.main", Cmd.Flags().Lookup("main")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.data", Cmd.Flags().Lookup("data")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.output", Cmd.Flags().Lookup("output")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.rate", Cmd.Flags().Lookup("rate")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.gpu", Cmd.Flags().Lookup("gpu")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.ram", Cmd.Flags().Lookup("ram")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.disk", Cmd.Flags().Lookup("disk")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.pcie", Cmd.Flags().Lookup("pcie")); err != nil {
			return err
		}
		return nil
	}(); err != nil {
		log.Printf("Run: error binding pflag: %v", err)
		panic(err)
	}

	NotebookCmd.Flags().String("config", ".emrys", "Path to config file (don't include extension). Defaults to .emrys")
	NotebookCmd.Flags().String("project", "", "User project (required)")
	NotebookCmd.Flags().String("requirements", "", "Path to requirements file (required)")
	NotebookCmd.Flags().String("main", "", "Path to main execution file")
	NotebookCmd.Flags().String("data", "", "Path to the data directory")
	NotebookCmd.Flags().String("output", "", "Path to save the output directory (required)")
	NotebookCmd.Flags().Float64("rate", 0, "Maximum $ / hr willing to pay for job")
	NotebookCmd.Flags().String("gpu", "k80", "Minimum acceptable gpu for job. Defaults to k80")
	NotebookCmd.Flags().String("ram", "8gb", "Minimum acceptable gb of available ram for job. Defaults to 8gb")
	NotebookCmd.Flags().String("disk", "25gb", "Minimum acceptable gb of disk space for job. Defaults to 25gb")
	NotebookCmd.Flags().String("pcie", "8x", "Minimum acceptable gpu pci-e for job. Defaults to 8x")
	NotebookCmd.Flags().SortFlags = false
	if err := func() error {
		if err := viper.BindPFlag("config", NotebookCmd.Flags().Lookup("config")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.project", NotebookCmd.Flags().Lookup("project")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.requirements", NotebookCmd.Flags().Lookup("requirements")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.main", NotebookCmd.Flags().Lookup("main")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.data", NotebookCmd.Flags().Lookup("data")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.output", NotebookCmd.Flags().Lookup("output")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.rate", NotebookCmd.Flags().Lookup("rate")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.gpu", NotebookCmd.Flags().Lookup("gpu")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.ram", NotebookCmd.Flags().Lookup("ram")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.disk", NotebookCmd.Flags().Lookup("disk")); err != nil {
			return err
		}
		if err := viper.BindPFlag("user.pcie", NotebookCmd.Flags().Lookup("pcie")); err != nil {
			return err
		}
		return nil
	}(); err != nil {
		log.Printf("Notebook: error binding pflag: %v", err)
		panic(err)
	}
}

// Cmd exports run subcommand to root
var Cmd = &cobra.Command{
	Use:   "run",
	Short: "Dispatch a deep learning job",
	Long: "Syncs the appropriate execution files & data " +
		"with the central server, then locates the cheapest " +
		"spare GPU cycles on the internet to execute your job" +
		"\n\nReport bugs to support@emrys.io",
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
		if os.Geteuid() == 0 {
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
		viper.AddConfigPath("$HOME")
		viper.AddConfigPath("$HOME/.config/emrys")
		viper.AddConfigPath(".")
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

		j := &userJob{
			client:       client,
			authToken:    authToken,
			project:      viper.GetString("user.project"),
			requirements: viper.GetString("user.requirements"),
			main:         viper.GetString("user.main"),
			data:         viper.GetString("user.data"),
			output:       viper.GetString("user.output"),
			gpuRaw:       viper.GetString("user.gpu"),
			ramStr:       viper.GetString("user.ram"),
			diskStr:      viper.GetString("user.disk"),
			pcieStr:      viper.GetString("user.pcie"),
			specs: &job.Specs{
				Rate: viper.GetFloat64("user.rate"),
			},
		}
		if err := j.validateAndTransform(); err != nil {
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
				if auctionComplete {
					// j.cancel returns when job successfully canceled
					if err := j.cancel(u); err != nil {
						log.Printf("Notebook: error canceling: %v", err)
						return
					}
				} else {
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

		if err := j.send(ctx, u); err != nil {
			log.Printf("Run: error sending requirements: %v", err)
			return
		}

		go func() {
			for {
				if err := token.Monitor(ctx, client, u, &j.authToken, refreshAt); err != nil {
					log.Printf("Token: refresh error: %v", err)
				}
				select {
				case <-ctx.Done():
					return
				default:
				}
			}
		}()

		if err := checkContextCanceled(ctx); err != nil {
			return
		}
		errCh := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go j.buildImage(ctx, &wg, errCh, u)
		go j.syncData(ctx, &wg, errCh, u)
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-ctx.Done():
			return
		case <-done:
		case <-errCh:
			return
		}

		if err := j.runAuction(ctx, u); err != nil {
			return // already logged
		}
		auctionComplete = true

		if jobCanceled {
			return
		}
		outputDir := filepath.Join(j.output, j.id)
		if err = os.MkdirAll(outputDir, 0755); err != nil {
			log.Printf("Output data: error making output dir %v: %v", outputDir, err)
			return
		}

		log.Printf("Executing job %s...\n", j.id)
		if err := j.streamOutputLog(ctx, u); err != nil {
			log.Printf("Output log: error: %v", err)
			return
		}
		// TODO: replace w/ longpoll asking when output data has posted?
		// think this currently streams though so probably not
		time.Sleep(buffer)
		if err := j.downloadOutputData(ctx, u); err != nil {
			log.Printf("Output data: error: %v", err)
			return
		}

		if os.Geteuid() == 0 {
			if err = os.Chown(j.output, uid, gid); err != nil {
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

func checkContextCanceled(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
