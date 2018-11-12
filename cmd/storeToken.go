package cmd

import (
	"io/ioutil"
	"log"
	"os"
	"os/user"
	"path"
	"path/filepath"
)

func storeToken(t string) error {
	u, err := user.Current()
	if err != nil {
		log.Printf("Failed to get current user: %v", err)
		return err
	}
	configDir := path.Join(u.HomeDir, ".config", "emrys")
	p := path.Join(configDir, "access_token")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		log.Printf("Failed to make directory %s to save login token: %v", configDir, err)
		return err
	}
	if err := ioutil.WriteFile(p, []byte(t), 0600); err != nil {
		log.Printf("Failed to write login token to disk at %s: %v", p, err)
		return err
	}
	return nil
}
