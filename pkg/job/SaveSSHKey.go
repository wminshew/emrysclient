package job

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/user"
	"path"
)

// SaveSSHKey saves the job's ssh-key to disk
func (j *Job) SaveSSHKey() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("getting current user: %v", err)
	}
	if os.Geteuid() == 0 && os.Getenv("SUDO_USER") != "" {
		u, err = user.Lookup(os.Getenv("SUDO_USER"))
		if err != nil {
			return "", fmt.Errorf("getting current sudo user: %v", err)
		}
	}
	dir := path.Join(u.HomeDir, ".config", "emrys")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("making directory %s: %v", dir, err)
	}
	p := path.Join(dir, fmt.Sprintf("%s-ssh-key-user", j.ID))
	if err := ioutil.WriteFile(p, j.SSHKey, 0600); err != nil {
		return "", fmt.Errorf("writing ssh-key to disk at %s: %v", p, err)
	}
	return p, nil
}
