package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/creds"
	"golang.org/x/crypto/ssh/terminal"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
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
	Short: "Log in to emrys",
	Long: "After receiving a valid email and password, " +
		"login save a JSON web token (JWT) locally. By default, " +
		"the token expires in 7 days.",
	Run: func(cmd *cobra.Command, args []string) {
		if err := checkVersion(); err != nil {
			fmt.Printf("Version error: %v\n", err)
			return
		}

		c := &creds.User{}
		userLogin(c)
		c.Duration = strconv.Itoa(viper.GetInt("save"))

		bodyBuf := &bytes.Buffer{}
		err := json.NewEncoder(bodyBuf).Encode(c)
		if err != nil {
			fmt.Printf("Failed to encode email & password: %v\n", err)
			return
		}
		s := "https"
		h := resolveHost()
		p := path.Join("user", "login")
		u := url.URL{
			Scheme: s,
			Host:   h,
			Path:   p,
		}
		client := &http.Client{}
		resp, err := client.Post(u.String(), "text/plain", bodyBuf)
		if err != nil {
			fmt.Printf("Failed to POST %v: %v\n", u.Path, err)
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

		loginResp := creds.LoginResp{}
		err = json.NewDecoder(resp.Body).Decode(&loginResp)
		if err != nil {
			fmt.Printf("Failed to decode response: %v\n", err)
			return
		}

		storeToken(loginResp.Token)
		fmt.Printf("Success! Your login token will expire in %s days\n", c.Duration)
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
		fmt.Printf("\nFailed to read password from console: %v\n", err)
		return
	}
	c.Password = strings.TrimSpace(string(bytePassword))
	fmt.Println()
}

func storeToken(t string) {
	var perm os.FileMode
	perm = 0755
	user, err := user.Current()
	if err != nil {
		fmt.Printf("Failed to get current user: %v\n", err)
		return
	}
	dir := path.Join(user.HomeDir, ".config", "emrys")
	err = os.MkdirAll(dir, perm)
	if err != nil {
		fmt.Printf("Failed to make directory %s to save login token: %v\n", dir, err)
		return
	}
	path := path.Join(dir, "jwt")
	if err := ioutil.WriteFile(path, []byte(t), perm); err != nil {
		fmt.Printf("Failed to write login token to disk at %s: %v", path, err)
		return
	}
}
