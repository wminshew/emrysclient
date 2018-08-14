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
	var perm os.FileMode
	perm = 0755
	u, err := user.Current()
	if err != nil {
		log.Printf("Failed to get current user: %v\n", err)
		return err
	}
	configDir := path.Join(u.HomeDir, ".config", "emrys")
	p := path.Join(configDir, "jwt")
	if err := os.MkdirAll(filepath.Dir(p), perm); err != nil {
		log.Printf("Failed to make directory %s to save login token: %v\n", configDir, err)
		return err
	}
	if err := ioutil.WriteFile(p, []byte(t), perm); err != nil {
		log.Printf("Failed to write login token to disk at %s: %v", p, err)
		return err
	}
	return nil
}
