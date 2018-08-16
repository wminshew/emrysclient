package cmd

import (
	"bytes"
	"encoding/json"
	"github.com/cenkalti/backoff"
	"github.com/spf13/cobra"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/creds"
	"io/ioutil"
	"log"
	"net/http"
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
		s := "https"
		h := resolveHost()
		u := url.URL{
			Scheme: s,
			Host:   h,
		}
		if err := checkVersion(client, u); err != nil {
			log.Printf("Version error: %v\n", err)
			return
		}

		c := &creds.User{}
		userLogin(c)

		bodyBuf := &bytes.Buffer{}
		if err := json.NewEncoder(bodyBuf).Encode(c); err != nil {
			log.Printf("Failed to encode email & password: %v\n", err)
			return
		}
		p := path.Join("user")
		u.Path = p
		var resp *http.Response
		operation := func() error {
			var err error
			resp, err = client.Post(u.String(), "text/plain", bodyBuf)
			return err
		}
		if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
			log.Printf("Error POST %v: %v\n", u.String(), err)
			return
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode != http.StatusOK {
			log.Printf("Response error header: %v\n", resp.Status)
			b, _ := ioutil.ReadAll(resp.Body)
			log.Printf("Response error detail: %s", b)
			return
		}

		log.Printf("We emailed a confirmation link to %s. Please follow the link "+
			"to finish registering (if you can't find the email, please check your spam folder just in case.)", c.Email)
	},
}
