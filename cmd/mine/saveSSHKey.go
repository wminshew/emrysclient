package mine

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/user"
	"path"
)

// saveSSHKey saves the job's ssh-key to disk
func (w *worker) saveSSHKey() (string, error) {
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
	dir := path.Join(u.HomeDir, ".config", "emrys")
	// TODO: should be able to remove this mkdir call
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("making directory %s: %v", dir, err)
	}
	p := path.Join(dir, fmt.Sprintf("%s-ssh-key-miner", w.jID))
	if err := ioutil.WriteFile(p, w.sshKey, 0600); err != nil {
		return "", fmt.Errorf("writing ssh-key to disk at %s: %v", p, err)
	}
	return p, nil
}
