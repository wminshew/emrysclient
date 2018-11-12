package cmd

import (
	"fmt"
	"github.com/wminshew/emrys/pkg/check"
	"io"
	"os"
	"os/user"
	"path"
	"path/filepath"
)

func storeProjectDataMetadata(project string, r io.Reader) error {
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
	configDir := path.Join(u.HomeDir, ".config", "emrys")
	p := path.Join(configDir, "projects", project, ".data_sync_metadata")
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("making directory: %v", err)
	}
	f, err := os.Create(p)
	if err != nil {
		return fmt.Errorf("creating file: %v", err)
	}
	defer check.Err(f.Close)
	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("copying file: %v", err)
	}
	return nil
}
