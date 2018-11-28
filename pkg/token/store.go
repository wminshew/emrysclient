package token

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/user"
	"path"
)

// Store token t on disk
func Store(t string) error {
	u, err := user.Current()
	if err != nil {
		return fmt.Errorf("getting current user: %v", err)
	}
	if os.Geteuid() == 0 {
		u, err = user.Lookup(os.Getenv("SUDO_USER"))
		if err != nil {
			return fmt.Errorf("getting current sudo user: %v", err)
		}
	}
	dir := path.Join(u.HomeDir, ".config", "emrys")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("making directory %s: %v", dir, err)
	}
	p := path.Join(dir, "access_token")
	if err := ioutil.WriteFile(p, []byte(t), 0600); err != nil {
		return fmt.Errorf("writing token to disk at %s: %v", p, err)
	}
	return nil
}
