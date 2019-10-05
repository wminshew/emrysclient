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

// UserVer is the semver user client version
var UserVer = semver.Version{
	Major: 0,
	Minor: 13,
	Patch: 0,
}

// MinerVer is the semver miner client version
var MinerVer = semver.Version{
	Major: 0,
	Minor: 13,
	Patch: 1,
}

const (
	maxRetries = 10
)

// Cmd exports version subcommand to root
var Cmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Long:  "Show version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("emrys user version %s\n", UserVer.String())
		fmt.Printf("emrys miner version %s\n", MinerVer.String())
	},
}

// CheckRun verifies the run subcommand version is compatible with the server
func CheckRun(ctx context.Context, client *http.Client, u url.URL) error {
	p := path.Join("user", "version")
	u.Path = p
	latestUserVer, err := GetServerVersion(ctx, client, u)
	if err != nil {
		return err
	}
	if UserVer.Major < latestUserVer.Major {
		return fmt.Errorf("user version %v incompatible with latest (%s) and must be updated", UserVer, latestUserVer)
	}
	if UserVer.LT(latestUserVer) {
		log.Printf("Run: version warning: your user version %v should be updated to the latest (%v)\n"+
			"Please execute emrys update", UserVer, latestUserVer)
	}

	return nil
}

// CheckMine verifies the mine subcommand version is compatible with the server
func CheckMine(ctx context.Context, client *http.Client, u url.URL) error {
	p := path.Join("miner", "version")
	u.Path = p
	latestMinerVer, err := GetServerVersion(ctx, client, u)
	if err != nil {
		return err
	}
	if MinerVer.Major < latestMinerVer.Major {
		return fmt.Errorf("your miner version %v is incompatible with the latest and must be updated to continue (%v)", MinerVer, latestMinerVer)
	}
	if MinerVer.LT(latestMinerVer) {
		log.Printf("Mine: version warning: your miner version %v should be updated to the latest (%v)\n"+
			"Please execute emrys update", MinerVer, latestMinerVer)
	}

	return nil
}

var validVersionPaths = map[string]struct{}{
	"user/version":  struct{}{},
	"miner/version": struct{}{},
}

// GetServerVersion returns the appropriate latest client version given the correct URL
func GetServerVersion(ctx context.Context, client *http.Client, u url.URL) (semver.Version, error) {
	if _, ok := validVersionPaths[u.Path]; !ok {
		return semver.Version{}, fmt.Errorf("invalid version path")
	}
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
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Version error: %v", err)
			log.Printf("Retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		return semver.Version{}, err
	}

	return semver.Make(verResp.Version)
}
