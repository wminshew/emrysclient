package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"github.com/wminshew/check"
	"golang.org/x/crypto/ssh/terminal"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/user"
	"path"
	"strings"
	"syscall"
)

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginSuccess struct {
	Token string `json:"token"`
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to emrysminer",
	Long: `After receiving a valid email and password, 
login saves a JSON web token (JWT) locally.
By default, the token expires in 24 hours.`,
	Run: func(cmd *cobra.Command, args []string) {
		creds := &credentials{}
		minerLogin(creds)

		bodyBuf := &bytes.Buffer{}
		err := json.NewEncoder(bodyBuf).Encode(creds)
		if err != nil {
			log.Printf("Failed to encode email & password: %v\n", err)
			return
		}
		h := resolveHost()
		u := url.URL{
			Scheme: "https",
			Host:   h,
			Path:   "/miner/login",
		}
		client := resolveClient()
		resp, err := client.Post(u.String(), "text/plain", bodyBuf)
		if err != nil {
			log.Printf("Failed to POST: %v\n", err)
			log.Printf("URL: %v\n", u)
			log.Printf("Body: %v\n", bodyBuf)
			return
		}
		defer check.Err(resp.Body.Close)

		if appEnv == "dev" {
			respDump, err := httputil.DumpResponse(resp, true)
			if err != nil {
				log.Println(err)
			}
			log.Println(string(respDump))
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("Request error: %v\n", resp.Status)
			return
		}

		loginSuccess := loginSuccess{}
		err = json.NewDecoder(resp.Body).Decode(&loginSuccess)
		if err != nil {
			log.Printf("Failed to decode response: %v\n", err)
			return
		}

		storeToken(loginSuccess.Token)
	},
}

func minerLogin(c *credentials) {
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

func storeToken(t string) {
	var perm os.FileMode
	perm = 0755
	user, err := user.Current()
	if err != nil {
		log.Fatalf("Failed to get current user: %v\n", err)
	}
	dir := path.Join(user.HomeDir, ".config", "emrysminer")
	fmt.Print(dir)
	err = os.MkdirAll(dir, perm)
	if err != nil {
		log.Fatalf("Failed to make directory %s to save login token: %v\n", dir, err)
	}
	path := path.Join(dir, "jwt")
	if err := ioutil.WriteFile(path, []byte(t), perm); err != nil {
		log.Fatalf("Failed to write login token to disk at %s: %v", path, err)
	}
}
