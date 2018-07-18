package cmd

import (
	"fmt"
	"io/ioutil"
	"os/user"
	"path"
)

func getToken() (string, error) {
	user, err := user.Current()
	if err != nil {
		fmt.Printf("Failed to get current user: %v\n", err)
		return "", err
	}
	path := path.Join(user.HomeDir, ".config", "emrys", "jwt")
	byteToken, err := ioutil.ReadFile(path)
	if err != nil {
		fmt.Printf("Failed to read token at %s: %v", path, err)
		return "", err
	}
	return string(byteToken), nil
}
