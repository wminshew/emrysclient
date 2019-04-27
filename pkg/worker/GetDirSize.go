package worker

import (
	"os"
	"path/filepath"
)

// GetDirSize returns the size of the directory in bytes
func GetDirSize(dir string) (int64, error) {
	var dirSize int64
	if err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			dirSize += info.Size()
		}
		return nil
	}); err != nil {
		return 0, err
	}
	return dirSize, nil
}
