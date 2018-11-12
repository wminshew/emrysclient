package cmd

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/user"
	"path"
)

func getToken() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("getting current user: %v", err)
	}
	if os.Geteuid() == 0 {
		u, err = user.Lookup(os.Getenv("SUDO_USER"))
		if err != nil {
			return "", fmt.Errorf("getting current sudo user: %v", err)
		}
	}
	p := path.Join(u.HomeDir, ".config", "emrys", "access_token")
	byteToken, err := ioutil.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("reading token at %s: %v", p, err)
	}
	return string(byteToken), nil
}
