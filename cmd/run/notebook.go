package run

import (
	"context"
	"fmt"
	"github.com/dgrijalva/jwt-go"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wminshew/emrys/pkg/job"
	"github.com/wminshew/emrysclient/cmd/version"
	// "github.com/wminshew/emrysclient/pkg/job"
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

// NotebookCmd exports notebook subcommand to root
var NotebookCmd = &cobra.Command{
	Use:   "notebook",
	Short: "Create a local jupyter notebook executing on a remote gpu",
	Long: "Syncs the appropriate requirements & data " +
		"with the central server, then locates the cheapest " +
		"spare GPU cycles on the internet to begin a jupyter notebook" +
		"\n\nReport bugs to support@emrys.io",
	Run: func(cmd *cobra.Command, args []string) {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-stop
			log.Printf("Cancellation request received: please wait for notebook to successfully cancel\n")
			log.Printf("Warning: failure to successfully cancel notebook may result in undesirable charges\n")
			cancel()
		}()

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

		client := &http.Client{}
		s := "https"
		h := "api.emrys.io"
		u := url.URL{
			Scheme: s,
			Host:   h,
		}

		if err := version.CheckRun(ctx, client, u); err != nil {
			log.Printf("Notebook: version error: %v", err)
			log.Printf("Please execute emrys update")
			return
		}

		viper.SetConfigName(viper.GetString("config"))
		viper.AddConfigPath("$HOME")
		viper.AddConfigPath("$HOME/.config/emrys")
		viper.AddConfigPath(".")
		if err := viper.ReadInConfig(); err != nil {
			log.Printf("Notebook: error reading config file: %v", err)
			return
		}

		// TODO: move to pkg/job; rename job.Spec to something else
		j := &userJob{
			client:       client,
			authToken:    authToken,
			project:      viper.GetString("user.project"),
			requirements: viper.GetString("user.requirements"),
			main:         viper.GetString("user.main"),
			notebook:     true,
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
			log.Printf("Notebook: invalid requirements: %v", err)
			return
		}

		if err := j.send(ctx, u); err != nil {
			log.Printf("Notebook: error sending requirements: %v", err)
			return
		}
		completed := false
		defer func() {
			if !completed {
				if err := j.cancel(u); err != nil {
					log.Printf("Notebook: error canceling: %v", err)
					return
				}
			}
		}()
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

		sshKeyFile, err := j.saveSSHKey()
		if err != nil {
			log.Printf("Notebook: error saving key: %v", err)
			return
		}
		// defer func() {
		// 	if err := os.Remove(sshKeyFile); err != nil {
		// 		log.Printf("Notebook: error removing ssh key: %v", err)
		// 		return
		// 	}
		// }()

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

		if err := checkContextCanceled(ctx); err != nil {
			return
		}
		outputDir := filepath.Join(j.output, j.id)
		if err = os.MkdirAll(outputDir, 0755); err != nil {
			log.Printf("Output data: error making output dir %v: %v", outputDir, err)
			return
		}

		// TODO: hmm
		log.Printf("Executing notebook %s...\n", j.id)
		// if err := j.streamOutputLog(ctx, u); err != nil {
		// 	log.Printf("Output log: error: %v", err)
		// 	return
		// }
		if err := j.sshLocalForward(ctx, sshKeyFile); err != nil {
			log.Printf("ssh: error: %v", err)
			return
		}
		time.Sleep(buffer)
		if err := j.downloadOutputData(ctx, u); err != nil {
			log.Printf("Output data: error: %v", err)
			return
		}

		completed = true

		if os.Geteuid() == 0 {
			if err = os.Chown(j.output, uid, gid); err != nil {
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

		log.Printf("Complete!\n")
	},
}
