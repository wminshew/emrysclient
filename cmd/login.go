package cmd

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
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

var (
	env = os.Getenv("ENV")
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
	Short: "Log in to emrys",
	Long: `After receiving a valid email and password,
	login save a JSON web token (JWT) locally set to
	expire in 24 hours.`,
	Run: func(cmd *cobra.Command, args []string) {
		creds := &credentials{}

		reader := bufio.NewReader(os.Stdin)
		fmt.Printf("Email: ")
		email, _ := reader.ReadString('\n')
		creds.Email = strings.TrimSpace(email)

		fmt.Printf("Password: ")
		bytePassword, err := terminal.ReadPassword(int(syscall.Stdin))
		if err != nil {
			log.Fatalf("\nFailed to read password from console: %v\n", err)
		}
		creds.Password = strings.TrimSpace(string(bytePassword))
		fmt.Println()

		bodyBuf := &bytes.Buffer{}
		err = json.NewEncoder(bodyBuf).Encode(creds)
		if err != nil {
			log.Fatalf("Failed to encode email & password: %v\n", err)
		}
		uPath, _ := url.Parse("/user/signin")
		url := baseURL.ResolveReference(uPath)
		// TODO: make this a clean distinction between DEV / PROD
		var client *http.Client
		if env == "DEV" {
			CA_Pool := x509.NewCertPool()
			serverCert, err := ioutil.ReadFile("./devCert.crt")
			if err != nil {
				log.Fatalf("Could not load dev certificate: %v\n", err)
			}
			CA_Pool.AppendCertsFromPEM(serverCert)
			config := &tls.Config{RootCAs: CA_Pool}
			tr := &http.Transport{TLSClientConfig: config}
			client = &http.Client{Transport: tr}
		} else {
			client = &http.Client{}
		}
		resp, err := client.Post(url.String(), "text/plain", bodyBuf)
		// resp, err := http.Post(url.String(), "text/plain", bodyBuf)
		if err != nil {
			log.Fatalf("Failed to POST: %v\n", err)
			log.Fatalf("URL: %v\n", url)
			log.Fatalf("Body: %v\n", bodyBuf)
		}
		defer resp.Body.Close()

		if env == "DEV" {
			respDump, err := httputil.DumpResponse(resp, true)
			if err != nil {
				log.Println(err)
			}
			log.Println(string(respDump))
		}

		if resp.StatusCode != http.StatusOK {
			log.Fatalf("Request error: %v\n", resp.Status)
		}

		loginSuccess := loginSuccess{}
		err = json.NewDecoder(resp.Body).Decode(&loginSuccess)
		if err != nil {
			log.Fatalf("Failed to decode response: %v\n", err)
		}

		var perm os.FileMode
		perm = 0755
		user, err := user.Current()
		if err != nil {
			log.Fatalf("Failed to get current user: %v\n", err)
		}
		dir := path.Join(user.HomeDir, ".config", "emrys")
		fmt.Print(dir)
		os.MkdirAll(dir, perm)
		if err != nil {
			log.Fatalf("Failed to make directory %s to save login token: %v\n", dir, err)
		}
		path := path.Join(dir, "jwt")
		if err := ioutil.WriteFile(path, []byte(loginSuccess.Token), perm); err != nil {
			log.Fatalf("Failed to write login token to disk at %s: %v", path, err)
		}
	},
}
