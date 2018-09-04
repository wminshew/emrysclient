package cmd

import (
	"encoding/json"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/job"
	"io"
	"log"
	"os"
	"os/user"
	"path"
)

func getProjectDataMetadata(project string, dataJSON *map[string]job.FileMetadata) error {
	u, err := user.Current()
	if err != nil {
		log.Printf("Failed to get current user: %v", err)
		return err
	}
	configDir := path.Join(u.HomeDir, ".config", "emrys")
	p := path.Join(configDir, "projects", project, ".data_sync_metadata")
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return nil
	}
	f, err := os.Open(p)
	if err != nil {
		log.Printf("Failed to open file %s to get project %s metadata: %v", p, project, err)
		return err
	}
	defer check.Err(f.Close)
	if err := json.NewDecoder(f).Decode(dataJSON); err != nil && err != io.EOF {
		log.Printf("Error decoding data directory as JSON: %v", err)
		return err
	}
	return nil
}
