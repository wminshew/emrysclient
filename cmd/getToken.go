package cmd

import (
	"io/ioutil"
	"log"
	"os/user"
	"path"
)

func getToken() string {
	user, err := user.Current()
	if err != nil {
		log.Fatalf("Failed to get current user: %v\n", err)
	}
	path := path.Join(user.HomeDir, ".config", "emrys", "jwt")
	byteToken, err := ioutil.ReadFile(path)
	if err != nil {
		log.Fatalf("Failed to read token at %s: %v", path, err)
	}
	return string(byteToken)
}
