package cmd

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
	// "github.com/mholt/archiver"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/job"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	// "time"
)

func syncData(ctx context.Context, client *http.Client, u url.URL, uID, project, jID, authToken string, dataDir string) {
	log.Printf("Syncing data...\n")
	m := "POST"
	h := "data.emrys.io"
	p := path.Join("user", uID, "project", project, "job", jID)
	u.Host = h
	u.Path = p

	var req *http.Request
	var resp *http.Response
	operation := func() error {
		r, w := io.Pipe()
		storeR, storeW := io.Pipe()
		tee := io.TeeReader(r, storeW)
		go func() {
			defer check.Err(w.Close)
			if dataDir == "" {
				log.Printf("No data directory provided.\n")
				return
			}
			oldMetadata := make(map[string]job.FileMetadata)
			if err := getProjectDataMetadata(project, &oldMetadata); err != nil {
				log.Printf("Error getting data directory metadata: %v\n", err)
				return
			}

			newMetadata := make(map[string]job.FileMetadata)
			if err := filepath.Walk(dataDir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() {
					return nil
				}

				rP, err := filepath.Rel(dataDir, path)
				if err != nil {
					return err
				}
				mT := info.ModTime().UnixNano()
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				defer check.Err(f.Close)
				if oldFileMD, ok := oldMetadata[rP]; ok {
					if oldFileMD.ModTime == mT {
						newMetadata[rP] = oldFileMD
						return nil
					}
				}
				h := md5.New()
				if _, err := io.Copy(h, f); err != nil {
					return err
				}
				hStr := base64.StdEncoding.EncodeToString(h.Sum(nil))
				fileMD := job.FileMetadata{
					ModTime: mT,
					Hash:    hStr,
				}
				newMetadata[rP] = fileMD
				return nil
			}); err != nil {
				log.Printf("Error walking data directory %s: %v\n", dataDir, err)
				return
			}

			if err := json.NewEncoder(w).Encode(newMetadata); err != nil {
				log.Printf("Error encoding data directory as JSON: %v\n", err)
				return
			}
		}()
		go func() {
			if err := storeProjectDataMetadata(project, storeR); err != nil {
				log.Printf("Error storing data directory metadata: %v\n", err)
				return
			}
		}()
		var err error
		if req, err = http.NewRequest(m, u.String(), tee); err != nil {
			log.Printf("Error creating request %v %v: %v\n", m, p, err)
			return err
		}
		req = req.WithContext(ctx)
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

		if resp, err = client.Do(req); err != nil {
			log.Printf("Error executing request %v %v: %v\n", m, p, err)
			return err
		}
		return nil
	}
	if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
		log.Printf("Error %v %v: %v\n", req.Method, req.URL.Path, err)
		return
	}
	defer check.Err(resp.Body.Close)

	if resp.StatusCode != http.StatusOK {
		log.Printf("Failed %s %s\n", req.Method, req.URL.Path)
		log.Printf("Response error header: %v\n", resp.Status)
		b, _ := ioutil.ReadAll(resp.Body)
		log.Printf("Response error detail: %s\n", b)
		return
	}

	log.Printf("Data synced!\n")
}
