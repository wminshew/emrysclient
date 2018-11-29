package run

import (
	"context"
	"github.com/dgrijalva/jwt-go"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
)

func init() {
	Cmd.Flags().String("config", ".emrys", "Path to config file (don't include extension)")
	Cmd.Flags().String("project", "", "User project (required)")
	Cmd.Flags().String("requirements", "", "Path to requirements file (required)")
	Cmd.Flags().String("main", "", "Path to main execution file (required)")
	Cmd.Flags().String("data", "", "Path to the data directory (optional)")
	Cmd.Flags().String("output", "", "Path to save the output directory (required)")
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
		return nil
	}(); err != nil {
		log.Printf("Run: error binding pflag: %v", err)
		panic(err)
	}
}

// Cmd exports login subcommand to root
var Cmd = &cobra.Command{
	Use:   "run",
	Short: "Dispatch a deep learning job",
	Long: "Syncs the appropriate maining files & data " +
		"with the central server, then locates the cheapest " +
		"spare GPU cycles on the internet to execute your job" +
		"\n\nReport bugs to support@emrys.io",
	Run: func(cmd *cobra.Command, args []string) {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-stop
			log.Printf("Cancellation request received: please wait for job to successfully cancel\n")
			log.Printf("Warning: failure to successfully cancel job may result in undesirable charges\n")
			cancel()
		}()

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
		uID := claims.Subject
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

		client := &http.Client{}
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

		if err := version.CheckRun(ctx, client, u); err != nil {
			log.Printf("Run: version error: %v", err)
			return
		}

		viper.SetConfigName(viper.GetString("config"))
		viper.AddConfigPath("$HOME")
		viper.AddConfigPath("$HOME/.config/emrys")
		viper.AddConfigPath(".")
		if err := viper.ReadInConfig(); err != nil {
			log.Printf("Run: error reading config file: %v", err)
			return
		}

		j := &jobReq{
			project:      viper.GetString("user.project"),
			requirements: viper.GetString("user.requirements"),
			main:         viper.GetString("user.main"),
			data:         viper.GetString("user.data"),
			output:       viper.GetString("user.output"),
		}
		if err := j.validate(); err != nil {
			log.Printf("Run: invalid job requirements: %v", err)
			return
		}
		var jID string
		if jID, err = j.send(ctx, client, u, uID, authToken); err != nil {
			log.Printf("Run: error sending job requirements: %v", err)
			return
		}
		completed := false
		defer func() {
			if !completed {
				if err := j.cancel(client, u, uID, jID, authToken); err != nil {
					log.Printf("Run: error canceling job: %v", err)
					return
				}
			}
		}()

		if err := checkContextCanceled(ctx); err != nil {
			return
		}
		errCh := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go buildImage(ctx, &wg, errCh, client, u, uID, j.project, jID, authToken, j.main, j.requirements)
		go syncData(ctx, &wg, errCh, client, u, uID, j.project, jID, authToken, j.data)
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

		if err := runAuction(ctx, client, u, jID, authToken); err != nil {
			return // already logged
		}

		if err := checkContextCanceled(ctx); err != nil {
			return
		}
		outputDir := filepath.Join(j.output, jID)
		if err = os.MkdirAll(outputDir, 0755); err != nil {
			log.Printf("Output data: error making output dir %v: %v", outputDir, err)
			return
		}
		if os.Geteuid() == 0 {
			if err = os.Chown(j.output, uid, gid); err != nil {
				log.Printf("Run: error changing ownership: %v", err)
			}
			if err = os.Chown(outputDir, uid, gid); err != nil {
				log.Printf("Run: error changing ownership: %v", err)
			}
		}

		log.Printf("Executing job %s...\n", jID)
		if err := streamOutputLog(ctx, client, u, jID, authToken, j.output); err != nil {
			log.Printf("Output log: error: %v", err)
			return
		}
		buffer := 1 * time.Second
		time.Sleep(buffer)
		if err := downloadOutputData(ctx, client, u, jID, authToken, j.output); err != nil {
			log.Printf("Output data: error: %v", err)
			return
		}

		completed = true

		log.Printf("Complete!\n")
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
