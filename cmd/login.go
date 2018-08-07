package cmd

import (
	"bufio"
	"bytes"
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
		h := resolveHost()
		u := url.URL{
			Scheme: s,
			Host:   h,
		}
		if err := checkVersion(client, u); err != nil {
			log.Printf("Version error: %v\n", err)
			return
		}

		c := &creds.Miner{}
		minerLogin(c)
		c.Duration = strconv.Itoa(viper.GetInt("save"))

		bodyBuf := &bytes.Buffer{}
		if err := json.NewEncoder(bodyBuf).Encode(c); err != nil {
			log.Printf("Failed to encode email & password: %v\n", err)
			return
		}
		p := path.Join("miner", "login")
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
			log.Printf("Response error detail: %s\n", b)
			return
		}

		loginResp := creds.LoginResp{}
		if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
			log.Printf("Failed to decode response: %v\n", err)
			return
		}
		if err := storeToken(loginResp.Token); err != nil {
			log.Printf("Failed to store login token: %v\n", err)
			return
		}
		log.Printf("Success! Your login token will expire in %s days\n", c.Duration)
	},
}

func minerLogin(c *creds.Miner) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("Email: ")
	email, _ := reader.ReadString('\n')
	c.Email = strings.TrimSpace(email)

	fmt.Printf("Password: ")
	bytePassword, err := terminal.ReadPassword(int(syscall.Stdin))
	if err != nil {
		log.Printf("\nFailed to read password from console: %v\n", err)
		return
	}
	c.Password = strings.TrimSpace(string(bytePassword))
	fmt.Println()
}

func storeToken(t string) error {
	var perm os.FileMode
	perm = 0755
	u, err := user.Current()
	if err != nil {
		log.Printf("Failed to get current user: %v\n", err)
		return err
	}
	dir := path.Join(u.HomeDir, ".config", "emrysminer")
	if err := os.MkdirAll(dir, perm); err != nil {
		log.Printf("Failed to make directory %s to save login token: %v\n", dir, err)
		return err
	}
	p := path.Join(dir, "jwt")
	if err := ioutil.WriteFile(p, []byte(t), perm); err != nil {
		log.Printf("Failed to write login token to disk at %s: %v", p, err)
		return err
	}
	return nil
}
