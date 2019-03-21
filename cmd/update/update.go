package update

import (
	"context"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/mholt/archiver"
	"github.com/spf13/cobra"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrysclient/cmd/version"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"time"
)

const maxRetries = 5

// Cmd exports version subcommand to root
var Cmd = &cobra.Command{
	Use:   "update",
	Short: "Updates emrys client",
	Long:  "Updates emrys client",
	Run: func(cmd *cobra.Command, args []string) {
		if os.Geteuid() != 0 {
			log.Printf("Insufficient privileges. Are you root?\n")
			return
		}

		ctx := context.Background()
		client := &http.Client{}
		u := url.URL{
			Scheme: "https",
			Host:   "api.emrys.io",
		}
		p := path.Join("user", "version")
		u.Path = p
		latestUserVer, err := version.GetServerVersion(ctx, client, u)
		if err != nil {
			panic(err)
		}

		p = path.Join("miner", "version")
		u.Path = p
		latestMinerVer, err := version.GetServerVersion(ctx, client, u)
		if err != nil {
			panic(err)
		}

		if version.UserVer.LT(latestUserVer) || version.MinerVer.LT(latestMinerVer) {
			log.Printf("Downloading latest client...\n")
			currUser, err := user.Current()
			if err != nil {
				log.Printf("Error getting current user: %v", err)
				return
			}
			if os.Geteuid() == 0 {
				currUser, err = user.Lookup(os.Getenv("SUDO_USER"))
				if err != nil {
					log.Printf("Error getting current sudo user: %v", err)
					return
				}
			}
			tempDir := path.Join(currUser.HomeDir, ".emrys", ".temp")
			if err := os.MkdirAll(tempDir, 0755); err != nil {
				log.Printf("Error making directory: %v", err)
				return
			}
			defer func() {
				if err := os.RemoveAll(tempDir); err != nil {
					log.Printf("Error removing directory & children: %v", err)
				}
			}()

			u.Host = "www.emrys.io"
			p = path.Join("download", fmt.Sprintf("emrys_u%s_m%s.tar.gz", latestUserVer.String(), latestMinerVer.String()))
			u.Path = p
			operation := func() error {
				resp, err := client.Get(u.String())
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

				if err := archiver.TarGz.Read(resp.Body, tempDir); err != nil {
					return backoff.Permanent(err)
				}

				return nil
			}
			if err := backoff.RetryNotify(operation,
				backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries), ctx),
				func(err error, t time.Duration) {
					log.Printf("Update error: %v", err)
					log.Printf("Retrying in %s seconds\n", t.Round(time.Second).String())
				}); err != nil {
				log.Printf("Update error: %v", err)
				return
			}

			currExec, err := os.Executable()
			if err != nil {
				log.Printf("Error getting current executable: %v", err)
				return
			}

			currExec, err = filepath.EvalSymlinks(currExec)
			if err != nil {
				log.Printf("Error evaluating symlinks: %v", err)
				return
			}

			tempEmrysPath := filepath.Join(tempDir, "emrys")
			if err := os.Rename(tempEmrysPath, currExec); err != nil {
				log.Printf("Error renaming file: %v", err)
				return
			}

			log.Printf("Emrys updated:\n")
			log.Printf("  User version -> %s\n", latestUserVer.String())
			log.Printf("  Miner version -> %s\n", latestMinerVer.String())
		} else {
			log.Printf("Up to date!\n")
		}
	},
}
