package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/spf13/cobra"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/creds"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
)

var registerCmd = &cobra.Command{
	Use:   "register",
	Short: "Create a new user account",
	Long: "Submit your email and a password to create a new " +
		"account on https://emrys.io",
	Run: func(cmd *cobra.Command, args []string) {
		client := &http.Client{}
		if err := checkVersion(client); err != nil {
			fmt.Printf("Version error: %v\n", err)
			return
		}

		c := &creds.User{}
		userLogin(c)

		bodyBuf := &bytes.Buffer{}
		err := json.NewEncoder(bodyBuf).Encode(c)
		if err != nil {
			fmt.Printf("Failed to encode email & password: %v\n", err)
			return
		}
		s := "https"
		h := resolveHost()
		p := path.Join("user")
		u := url.URL{
			Scheme: s,
			Host:   h,
			Path:   p,
		}
		var resp *http.Response
		operation := func() error {
			var err error
			resp, err = client.Post(u.String(), "text/plain", bodyBuf)
			return err
		}
		expBackOff := backoff.NewExponentialBackOff()
		if err := backoff.Retry(operation, expBackOff); err != nil {
			fmt.Printf("Error POST %v: %v\n", u.String(), err)
			return
		}
		defer check.Err(resp.Body.Close)

		if appEnv == "dev" {
			respDump, err := httputil.DumpResponse(resp, true)
			if err != nil {
				fmt.Println(err)
			}
			fmt.Println(string(respDump))
		}

		if resp.StatusCode != http.StatusOK {
			fmt.Printf("Request error: %v\n", resp.Status)
			return
		}

		fmt.Printf("We emailed a confirmation link to %s. Please follow the link "+
			"to finish registering (if you can't find the email, please check your spam folder just in case.)", c.Email)
	},
}
