package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/spf13/cobra"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/creds"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"
)

var registerCmd = &cobra.Command{
	Use:   "register",
	Short: "Create a new miner account",
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
			log.Printf("Version error: %v", err)
			return
		}

		c := &creds.Miner{}
		minerLogin(c)
		bodyBuf := &bytes.Buffer{}
		if err := json.NewEncoder(bodyBuf).Encode(c); err != nil {
			log.Printf("Failed to encode email & password: %v", err)
			return
		}

		p := path.Join("miner")
		u.Path = p
		operation := func() error {
			resp, err := client.Post(u.String(), "text/plain", bodyBuf)
			if err != nil {
				return fmt.Errorf("%s %v: %v", "POST", u.Path, err)
			}
			defer check.Err(resp.Body.Close)

			if resp.StatusCode != http.StatusOK {
				b, _ := ioutil.ReadAll(resp.Body)
				return fmt.Errorf("server response: %s", b)
			}

			return nil
		}
		ctx := context.Background()
		if err := backoff.RetryNotify(operation, backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5), ctx),
			func(err error, t time.Duration) {
				log.Printf("Register error: %v", err)
				log.Printf("Trying again in %s seconds\n", t.Round(time.Second).String())
			}); err != nil {
			log.Printf("Register error: %v", err)
			os.Exit(1)
		}

		log.Printf("We emailed a confirmation link to %s. Please follow the link "+
			"to finish registering (if you can't find the email, please check your spam folder just in case.)", c.Email)
	},
}
