package notebook

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
	Cmd.Flags().String("config", ".emrys", "Path to config file (don't include extension). Defaults to .emrys")
	Cmd.Flags().String("project", "", "User project (required)")
	Cmd.Flags().String("requirements", "", "Path to requirements file (required)")
	Cmd.Flags().String("main", "", "Path to main execution file")
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
		log.Printf("Notebook: error binding pflag: %v", err)
		panic(err)
	}
}

// Cmd exports notebook subcommand to root
var Cmd = &cobra.Command{
	Use:   "notebook",
	Short: "Create a local jupyter notebook executing on a remote gpu",
	Long: "Syncs the appropriate requirements & data " +
		"with the central server, then locates the cheapest " +
		"spare GPU cycles on the internet to begin a jupyter notebook" +
		"\n\nReport bugs to support@emrys.io or with the feedback subcommand" +
		"\nIf you have any questions, please visit our forum https://forum.emrys.io " +
		"or slack channel https://emrysio.slack.com",
	Run: func(cmd *cobra.Command, args []string) {
		if os.Geteuid() != 0 {
			log.Printf("Insufficient privileges. Are you root?\n")
			return
		}

		authToken, err := token.Get()
		if err != nil {
			log.Printf("Notebook: error retrieving authToken: %v", err)
			return
		}
		claims := &jwt.StandardClaims{}
		if _, _, err := new(jwt.Parser).ParseUnverified(authToken, claims); err != nil {
			log.Printf("Notebook: error parsing authToken: %v", err)
			return
		}
		if err := claims.Valid(); err != nil {
			log.Printf("Notebook: invalid authToken: %v", err)
			log.Printf("Notebook: please login again.\n")
			return
		}
		exp := claims.ExpiresAt
		refreshAt := time.Unix(exp, 0).Add(token.RefreshBuffer)
		if refreshAt.Before(time.Now()) {
			log.Printf("Notebook: token too close to expiration, please login again.")
			return
		}

		var uid, gid int
		if os.Geteuid() == 0 {
			sudoUser, err := user.Lookup(os.Getenv("SUDO_USER"))
			if err != nil {
				log.Printf("Notebook: error getting current sudo user: %v", err)
				return
			}
			if uid, err = strconv.Atoi(sudoUser.Uid); err != nil {
				log.Printf("Notebook: error converting uid to int: %v", err)
				return
			}
			if gid, err = strconv.Atoi(sudoUser.Gid); err != nil {
				log.Printf("Notebook: error converting gid to int: %v", err)
				return
			}
		}

		viper.SetConfigName(viper.GetString("config"))
		viper.AddConfigPath("$HOME")
		viper.AddConfigPath("$HOME/.config/emrys")
		viper.AddConfigPath(".")
		if err := viper.ReadInConfig(); err != nil {
			log.Printf("Notebook: error reading config file: %v", err)
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
			Client:       client,
			AuthToken:    authToken,
			Project:      viper.GetString("user.project"),
			Requirements: viper.GetString("user.requirements"),
			Main:         viper.GetString("user.main"),
			Notebook:     true,
			Data:         viper.GetString("user.data"),
			Output:       viper.GetString("user.output"),
			GPURaw:       viper.GetString("user.gpu"),
			RAMStr:       viper.GetString("user.ram"),
			DiskStr:      viper.GetString("user.disk"),
			PCIEStr:      viper.GetString("user.pcie"),
			Specs: &specs.Specs{
				Rate: viper.GetFloat64("user.rate"),
			},
		}
		if err := j.ValidateAndTransform(); err != nil {
			log.Printf("Notebook: invalid requirements: %v", err)
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
				log.Printf("Cancellation request received: please wait for notebook to successfully cancel\n")
				log.Printf("Warning: failure to successfully cancel notebook may result in undesirable charges\n")
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
			log.Printf("Notebook: version error: %v", err)
			log.Printf("Please execute emrys update")
			return
		}

		if err := j.Send(ctx, u); err != nil {
			log.Printf("Notebook: error sending requirements: %v", err)
			return
		}
		go func() {
			for {
				if err := token.Monitor(ctx, client, u, &j.AuthToken, refreshAt); err != nil {
					log.Printf("Token: refresh error: %v", err)
				}
				select {
				case <-ctx.Done():
					return
				default:
				}
			}
		}()

		sshKeyFile, err := j.SaveSSHKey()
		if err != nil {
			log.Printf("Notebook: error saving key: %v", err)
			return
		}
		defer func() {
			if err := os.Remove(sshKeyFile); err != nil {
				log.Printf("Notebook: error removing ssh key: %v", err)
				return
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

		log.Printf("Executing notebook %s...\n", j.ID)
		sshCmd := j.SSHLocalForward(ctx, sshKeyFile)
		if err := sshCmd.Start(); err != nil {
			log.Printf("Notebook: error local forwarding requests: %v", err)
			return
		}
		defer func() {
			if err := sshCmd.Process.Kill(); err != nil {
				log.Printf("Notebook: error killing local forwarding process: %v", err)
				return
			}
		}()
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

		if os.Geteuid() == 0 {
			if err = os.Chown(j.Output, uid, gid); err != nil {
				log.Printf("Notebook: error changing ownership: %v", err)
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
				log.Printf("Notebook: error walking output directory: %v", err)
			}
		}

		if !jobCanceled {
			log.Printf("Complete!\n")
		}
	},
}
