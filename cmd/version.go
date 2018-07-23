package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/blang/semver"
	"github.com/cenkalti/backoff"
	"github.com/spf13/cobra"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/creds"
	"net/http"
	"net/url"
	"path"
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

func checkVersion(client *http.Client) error {
	s := "https"
	h := resolveHost()
	p := path.Join("user", "version")
	u := url.URL{
		Scheme: s,
		Host:   h,
		Path:   p,
	}
	var resp *http.Response
	operation := func() error {
		var err error
		resp, err = client.Get(u.String())
		return err
	}
	expBackOff := backoff.NewExponentialBackOff()
	if err := backoff.Retry(operation, expBackOff); err != nil {
		fmt.Printf("Error GET %v: %v\n", u.String(), err)
		return err
	}
	defer check.Err(resp.Body.Close)

	verResp := creds.VersionResp{}
	err := json.NewDecoder(resp.Body).Decode(&verResp)
	if err != nil {
		fmt.Printf("Failed to decode version response\n")
		return err
	}

	latestUserVer, err := semver.Make(verResp.Version)
	if err != nil {
		fmt.Printf("Failed to convert version response to semver\n")
		return err
	}
	if userVer.Major < latestUserVer.Major {
		return fmt.Errorf("your user version %v is incompatible with the latest and must be updated to continue (%v)", userVer, latestUserVer)
	}
	if userVer.LT(latestUserVer) {
		fmt.Printf("Warning: your user version %v should be updated to the latest (%v)\n", userVer, latestUserVer)
	}

	return nil
}
