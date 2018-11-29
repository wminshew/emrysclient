package version

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

var minerVer = semver.Version{
	Major: 0,
	Minor: 1,
	Patch: 0,
}

const maxBackoffRetries = 5

// Cmd exports version subcommand to root
var Cmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Long:  "Show version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("emrys run v%s\n", userVer.String())
		fmt.Printf("emrys mine v%s\n", minerVer.String())
	},
}

// CheckRun verifies the run subcommand version is compatible with the server
func CheckRun(ctx context.Context, client *http.Client, u url.URL) error {
	p := path.Join("user", "version")
	u.Path = p
	verResp := creds.VersionResp{}
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

		if err := json.NewDecoder(resp.Body).Decode(&verResp); err != nil {
			return fmt.Errorf("decoding response: %v", err)
		}
		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxBackoffRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Run: version error: %v", err)
			log.Printf("Run: retrying in %s seconds\n", t.Round(time.Second).String())
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
		log.Printf("Run: version warning: your user version %v should be updated to the latest (%v)\n", userVer, latestUserVer)
	}

	return nil
}

// CheckMine verifies the mine subcommand version is compatible with the server
func CheckMine(ctx context.Context, client *http.Client, u url.URL) error {
	p := path.Join("miner", "version")
	u.Path = p
	verResp := creds.VersionResp{}
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

		if err := json.NewDecoder(resp.Body).Decode(&verResp); err != nil {
			return fmt.Errorf("failed to decode response: %v", err)
		}
		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 3), ctx),
		func(err error, t time.Duration) {
			log.Printf("Mine: version error: %v", err)
			log.Printf("Mine: retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		return err
	}

	latestMinerVer, err := semver.Make(verResp.Version)
	if err != nil {
		return fmt.Errorf("failed to convert response to semver: %v", err)
	}
	if minerVer.Major < latestMinerVer.Major {
		return fmt.Errorf("your miner version %v is incompatible with the latest and must be updated to continue (%v)", minerVer, latestMinerVer)
	}
	if minerVer.LT(latestMinerVer) {
		log.Printf("Mine: version warning: your miner version %v should be updated to the latest (%v)\n", minerVer, latestMinerVer)
	}

	return nil
}
