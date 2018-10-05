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
	"path"
	"strconv"
	"strings"
	"syscall"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to emrys",
	Long: "After receiving a valid email and password, " +
		"login save a JSON web token (JWT) locally. By default, " +
		"the token expires in 7 days.",
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
			log.Printf("Version error: %v", err)
			return
		}

		c := &creds.User{}
		userLogin(c)
		c.Duration = strconv.Itoa(viper.GetInt("save"))

		p := path.Join("user", "login")
		u.Path = p
		loginResp := creds.LoginResp{}
		if err := func() error {
			bodyBuf := &bytes.Buffer{}
			if err := json.NewEncoder(bodyBuf).Encode(c); err != nil {
				return err
			}

			resp, err := client.Post(u.String(), "text/plain", bodyBuf)
			if err != nil {
				return err
			}
			defer check.Err(resp.Body.Close)

			if resp.StatusCode != http.StatusOK {
				b, _ := ioutil.ReadAll(resp.Body)
				return fmt.Errorf("server response: %s", b)
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
		log.Printf("Success! Your login token will expire in %s days\n", c.Duration)
	},
}

func userLogin(c *creds.User) {
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
