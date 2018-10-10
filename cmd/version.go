package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/blang/semver"
	"github.com/cenkalti/backoff"
	"github.com/spf13/cobra"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/creds"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path"
	"time"
)

var userVer = semver.Version{
	Major: 0,
	Minor: 1,
	Patch: 0,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of emrys",
	Long:  "All software has versions. This is emrys's",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("emrys v", userVer.String())
	},
}

func checkVersion(ctx context.Context, client *http.Client, u url.URL) error {
	p := path.Join("user", "version")
	u.Path = p
	verResp := creds.VersionResp{}
	operation := func() error {
		resp, err := client.Get(u.String())
		if err != nil {
			return err
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode != http.StatusOK {
			b, _ := ioutil.ReadAll(resp.Body)
			return fmt.Errorf("server response: %s", b)
		}

		if err := json.NewDecoder(resp.Body).Decode(&verResp); err != nil {
			return fmt.Errorf("decoding response: %v", err)
		}
		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5), ctx),
		func(err error, t time.Duration) {
			log.Printf("Version: error: %v", err)
			log.Printf("Version: retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		return err
	}

	latestUserVer, err := semver.Make(verResp.Version)
	if err != nil {
		return fmt.Errorf("converting response to semver: %v", err)
	}
	if userVer.Major < latestUserVer.Major {
		return fmt.Errorf("user version %v incompatible with latest (%s) and must be updated", userVer, latestUserVer)
	}
	if userVer.LT(latestUserVer) {
		log.Printf("Version: warning: your user version %v should be updated to the latest (%v)\n", userVer, latestUserVer)
	}

	return nil
}
