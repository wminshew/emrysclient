package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/creds"
	"golang.org/x/crypto/ssh/terminal"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to emrysminer",
	Long: "After receiving a valid email and password,login " +
		"saves a JSON web token (JWT) locally. By default, the " +
		"token expires in 24 hours.",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		client := &http.Client{}
		s := "https"
		h := "api.emrys.io"
		u := url.URL{
			Scheme: s,
			Host:   h,
		}
		if err := checkVersion(ctx, client, u); err != nil {
			log.Printf("Version error: %v", err)
			return
		}

		c := &creds.Account{}
		minerLogin(c)
		duration := strconv.Itoa(viper.GetInt("save"))

		p := path.Join("auth", "token")
		u.Path = p
		loginResp := creds.LoginResp{}
		operation := func() error {
			bodyBuf := &bytes.Buffer{}
			if err := json.NewEncoder(bodyBuf).Encode(c); err != nil {
				return err
			}

			req, err := http.NewRequest("POST", u.String(), bodyBuf)
			if err != nil {
				return err
			}

			q := req.URL.Query()
			q.Set("duration", duration)
			q.Set("grant_type", "password")
			req.URL.RawQuery = q.Encode()

			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer check.Err(resp.Body.Close)

			if resp.StatusCode == http.StatusBadGateway {
				return fmt.Errorf("server: temporary error")
			} else if resp.StatusCode >= 300 {
				b, _ := ioutil.ReadAll(resp.Body)
				return backoff.Permanent(fmt.Errorf("server: %v", b))
			}

			if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
				return backoff.Permanent(fmt.Errorf("decoding response: %v", err))
			}

			return nil
		}
		if err := backoff.RetryNotify(operation,
			backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxUploadRetries), ctx),
			func(err error, t time.Duration) {
				log.Printf("Login error: %v", err)
				log.Printf("Login error: retrying in %s seconds\n", t.Round(time.Second).String())
			}); err != nil {
			log.Printf("Login error: %v", err)
			os.Exit(1)
		}

		if err := storeToken(loginResp.Token); err != nil {
			log.Printf("Error storing login token: %v", err)
			os.Exit(1)
		}

		log.Printf("Success! Your login token will expire in %s days (you will not be logged off as long as you continue running the client)\n", duration)
	},
}

func minerLogin(c *creds.Account) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("Email: ")
	email, _ := reader.ReadString('\n')
	c.Email = strings.TrimSpace(email)

	fmt.Printf("Password: ")
	bytePassword, err := terminal.ReadPassword(syscall.Stdin)
	if err != nil {
		log.Printf("\nFailed to read password from console: %v", err)
		return
	}
	c.Password = strings.TrimSpace(string(bytePassword))
	fmt.Println()
}
