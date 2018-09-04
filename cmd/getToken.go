package cmd

import (
	"io/ioutil"
	"log"
	"os/user"
	"path"
)

func getToken() (string, error) {
	u, err := user.Current()
	if err != nil {
		log.Printf("Failed to get current user: %v", err)
		return "", err
	}
	p := path.Join(u.HomeDir, ".config", "emrys", "jwt")
	byteToken, err := ioutil.ReadFile(p)
	if err != nil {
		log.Printf("Failed to read token at %s: %v", p, err)
		return "", err
	}
	return string(byteToken), nil
}
