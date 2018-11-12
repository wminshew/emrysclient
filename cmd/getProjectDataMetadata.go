package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/job"
	"io"
	"os"
	"os/user"
	"path"
)

func getProjectDataMetadata(project string, dataJSON *map[string]job.FileMetadata) error {
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
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return nil
	}
	f, err := os.Open(p)
	if err != nil {
		return fmt.Errorf("opening file: %v", err)
	}
	defer check.Err(f.Close)
	if err := json.NewDecoder(f).Decode(dataJSON); err != nil && err != io.EOF {
		return fmt.Errorf("decoding json: %v", err)
	}
	return nil
}
