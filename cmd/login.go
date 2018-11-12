package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	"os/user"
	"path"
	"strconv"
	"strings"
	"syscall"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to emrysminer",
	Long: "After receiving a valid email and password,login " +
		"saves a JSON web token (JWT) locally. By default, the " +
		"token expires in 24 hours.",
	Run: func(cmd *cobra.Command, args []string) {
		client := &http.Client{}
		s := "https"
		h := "api.emrys.io"
		u := url.URL{
			Scheme: s,
			Host:   h,
		}
		if err := checkVersion(context.Background(), client, u); err != nil {
			log.Printf("Version error: %v", err)
			return
		}

		c := &creds.Account{}
		minerLogin(c)
		duration := strconv.Itoa(viper.GetInt("save"))

		p := path.Join("auth", "token")
		u.Path = p
		loginResp := creds.LoginResp{}
		if err := func() error {
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
			req.URL.RawQuery = q.Encode()

			resp, err := client.Do(req)
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

			if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
				return fmt.Errorf("failed to decode response: %v", err)
			}

			return nil
		}(); err != nil {
			log.Printf("Login error: %v", err)
			os.Exit(1)
		}

		if err := storeToken(loginResp.Token); err != nil {
			log.Printf("Failed to store login token: %v", err)
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
	bytePassword, err := terminal.ReadPassword(int(syscall.Stdin))
	if err != nil {
		log.Printf("\nFailed to read password from console: %v", err)
		return
	}
	c.Password = strings.TrimSpace(string(bytePassword))
	fmt.Println()
}

func storeToken(t string) error {
	u, err := user.Current()
	if err != nil {
		log.Printf("Failed to get current user: %v", err)
		return err
	}
	dir := path.Join(u.HomeDir, ".config", "emrys")
	if err := os.MkdirAll(dir, 0700); err != nil {
		log.Printf("Failed to make directory %s to save login token: %v\n", dir, err)
		return err
	}
	p := path.Join(dir, "access_token")
	if err := ioutil.WriteFile(p, []byte(t), 0600); err != nil {
		log.Printf("Failed to write login token to disk at %s: %v", p, err)
		return err
	}
	return nil
}
