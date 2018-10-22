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
	Short: "Create a new user account",
	Long: "Submit your email and a password to create a new " +
		"account on https://emrys.io",
	Run: func(cmd *cobra.Command, args []string) {
		client := &http.Client{}
		s := "https"
		h := "emrys.io"
		u := url.URL{
			Scheme: s,
			Host:   h,
		}
		ctx := context.Background()
		if err := checkVersion(ctx, client, u); err != nil {
			log.Printf("Version: error: %v", err)
			return
		}

		c := &creds.User{}
		userLogin(c)

		p := path.Join("user")
		u.Path = p
		operation := func() error {
			bodyBuf := &bytes.Buffer{}
			if err := json.NewEncoder(bodyBuf).Encode(c); err != nil {
				return err
			}

			resp, err := client.Post(u.String(), "text/plain", bodyBuf)
			if err != nil {
				return err
			}
			defer check.Err(resp.Body.Close)

			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadGateway {
				b, _ := ioutil.ReadAll(resp.Body)
				return fmt.Errorf("server response: %s", b)
			} else if resp.StatusCode == http.StatusBadGateway {
				return fmt.Errorf("server response: temporary error")
			}

			return nil
		}
		if err := backoff.RetryNotify(operation, backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5), ctx),
			func(err error, t time.Duration) {
				log.Printf("Register: error: %v", err)
				log.Printf("Retrying in %s seconds\n", t.Round(time.Second).String())
			}); err != nil {
			log.Printf("Register: error: %v", err)
			os.Exit(1)
		}

		log.Printf("We emailed a confirmation link to %s. Please follow the link "+
			"to finish registering (if you can't find the email, please check your spam folder just in case.)", c.Email)
	},
}
