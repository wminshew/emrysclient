package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/blang/semver"
	"github.com/cenkalti/backoff"
	"github.com/spf13/cobra"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/creds"
	"log"
	"net/http"
	"net/url"
	"path"
)

var minerVer = semver.Version{
	Major: 0,
	Minor: 1,
	Patch: 0,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of emrysminer",
	Long:  "All software has versions. This is emrysminer's",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("emrysminer v", minerVer.String())
	},
}

func checkVersion(client *http.Client, u url.URL) error {
	p := path.Join("miner", "version")
	u.Path = p
	var resp *http.Response
	operation := func() error {
		var err error
		resp, err = client.Get(u.String())
		return err
	}
	if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
		log.Printf("Error GET %v: %v\n", u.String(), err)
		return err
	}
	defer check.Err(resp.Body.Close)

	verResp := creds.VersionResp{}
	if err := json.NewDecoder(resp.Body).Decode(&verResp); err != nil {
		log.Printf("Failed to decode version response\n")
		return err
	}

	latestMinerVer, err := semver.Make(verResp.Version)
	if err != nil {
		log.Printf("Failed to convert version response to semver\n")
		return err
	}
	if minerVer.Major < latestMinerVer.Major {
		return fmt.Errorf("your miner version %v is incompatible with the latest and must be updated to continue (%v)", minerVer, latestMinerVer)
	}
	if minerVer.LT(latestMinerVer) {
		log.Printf("Warning: your miner version %v should be updated to the latest (%v)\n", minerVer, latestMinerVer)
	}

	return nil
}
