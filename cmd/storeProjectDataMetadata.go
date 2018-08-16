package cmd

import (
	"github.com/wminshew/emrys/pkg/check"
	"io"
	"log"
	"os"
	"os/user"
	"path"
	"path/filepath"
)

func storeProjectDataMetadata(project string, r io.Reader) error {
	var perm os.FileMode
	perm = 0755
	u, err := user.Current()
	if err != nil {
		log.Printf("Failed to get current user: %v\n", err)
		return err
	}
	configDir := path.Join(u.HomeDir, ".config", "emrys")
	p := path.Join(configDir, "projects", project, ".data_sync_metadata")
	if err := os.MkdirAll(filepath.Dir(p), perm); err != nil {
		log.Printf("Failed to make directory %s to save project %s metadata: %v\n", configDir, project, err)
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		log.Printf("Failed to create file %s to save project %s metadata: %v\n", p, project, err)
		return err
	}
	defer check.Err(f.Close)
	if _, err := io.Copy(f, r); err != nil {
		log.Printf("Failed to write project %s metadata to disk at %s: %v", project, p, err)
		return err
	}
	return nil
}
