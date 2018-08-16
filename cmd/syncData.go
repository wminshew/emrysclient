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
			defer check.Err(storeW.Close)
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
				if oldFileMd, ok := oldMetadata[rP]; ok {
					if oldFileMd.ModTime == mT {
						newMetadata[rP] = oldFileMd
						// check.Err(f.Close)
						return nil
					}
				}
				h := md5.New()
				if _, err := io.Copy(h, f); err != nil {
					// check.Err(f.Close)
					return err
				}
				// check.Err(f.Close)
				hStr := base64.StdEncoding.EncodeToString(h.Sum(nil))
				fileMd := job.FileMetadata{
					ModTime: mT,
					Hash:    hStr,
				}
				newMetadata[rP] = fileMd
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

	if resp.StatusCode != http.StatusOK {
		log.Printf("Failed %s %s\n", req.Method, req.URL.Path)
		log.Printf("Response error header: %v\n", resp.Status)
		b, _ := ioutil.ReadAll(resp.Body)
		log.Printf("Response error detail: %s\n", b)
		check.Err(resp.Body.Close)
		return
	}

	uploadList := []string{}
	if err := json.NewDecoder(resp.Body).Decode(&uploadList); err != nil && err != io.EOF {
		log.Printf("Failed to decode response body into string slice: %v\n", err)
		check.Err(resp.Body.Close)
		return
	}
	check.Err(resp.Body.Close)
	// TODO: remove list print
	log.Printf("Upload list: %v\n", uploadList)
	log.Printf("%d file(s) to upload\n", len(uploadList))

	// TODO: use some kind of worker queue with channels to avoid overloading server / user
	m = "PUT"
	for _, relPath := range uploadList {
		operation := func() error {
			var err error
			log.Printf(" Uploading file: %v\n", relPath)

			uploadFilepath := path.Join(dataDir, relPath)
			f, err := os.Open(uploadFilepath)
			if err != nil {
				log.Printf("Error opening file %v: %v\n", p, err)
				return err
			}
			// TODO: add zlib w/ pipe
			// r, w := io.Pipe()
			// zw := zlib.NewWriter(w)
			// go func() {
			//	_, err := io.Copy(zw, f); err != nil {
			//	log.Printf("Error copying file to zlib writer: %v\n", err)
			//	return
			// }
			// }()

			u.Path = path.Join(p, relPath)
			// if req, err = http.NewRequest(m, u.String(), r); err != nil {
			if req, err = http.NewRequest(m, u.String(), f); err != nil {
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

		if resp.StatusCode != http.StatusOK {
			log.Printf("Failed %s %s\n", req.Method, req.URL.Path)
			log.Printf("Response error header: %v\n", resp.Status)
			b, _ := ioutil.ReadAll(resp.Body)
			log.Printf("Response error detail: %s\n", b)
			return
		}

		log.Printf(" File uploaded: %v\n", relPath)
	}

	log.Printf("Data synced!\n")
}
