package job

import (
	"encoding/json"
	"fmt"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/job"
	"io"
	"os"
	"os/user"
	"path"
	"strconv"
)

func (j *Job) getProjectDataMetadata(dataJSON *map[string]job.FileMetadata) error {
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
	p := path.Join(configDir, "projects", j.Project, ".data_sync_metadata")
	if _, err = os.Stat(p); os.IsNotExist(err) {
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

func (j *Job) storeProjectDataMetadata(r io.Reader) error {
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
	var uid, gid int
	if uid, err = strconv.Atoi(u.Uid); err != nil {
		return fmt.Errorf("converting uid to int: %v", err)
	}
	if gid, err = strconv.Atoi(u.Gid); err != nil {
		return fmt.Errorf("converting gid to int: %v", err)
	}

	configDir := path.Join(u.HomeDir, ".config", "emrys")
	if err = os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("making directory: %v", err)
	}
	if err = os.Chown(configDir, uid, gid); err != nil {
		return fmt.Errorf("changing ownership: %v", err)
	}

	projectsDir := path.Join(configDir, "projects")
	if err = os.MkdirAll(projectsDir, 0755); err != nil {
		return fmt.Errorf("making directory: %v", err)
	}
	if err = os.Chown(projectsDir, uid, gid); err != nil {
		return fmt.Errorf("changing ownership: %v", err)
	}

	projectDir := path.Join(projectsDir, j.Project)
	if err = os.MkdirAll(projectDir, 0755); err != nil {
		return fmt.Errorf("making directory: %v", err)
	}
	if err = os.Chown(projectDir, uid, gid); err != nil {
		return fmt.Errorf("changing ownership: %v", err)
	}

	p := path.Join(projectDir, ".data_sync_metadata")
	f, err := os.Create(p)
	if err != nil {
		return fmt.Errorf("creating file: %v", err)
	}
	defer check.Err(f.Close)
	if err = os.Chown(p, uid, gid); err != nil {
		return fmt.Errorf("changing ownership: %v", err)
	}
	if _, err = io.Copy(f, r); err != nil {
		return fmt.Errorf("copying file: %v", err)
	}
	return nil
}
