package cmd

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
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
	"sync"
	"time"
)

func syncData(ctx context.Context, wg *sync.WaitGroup, errCh chan<- error, client *http.Client, u url.URL, uID, project, jID, authToken string, dataDir string) {
	defer wg.Done()
	log.Printf("Data: syncing...\n")

	bodyBuf := &bytes.Buffer{}
	var b []byte
	if err := func() error {
		if dataDir != "" {
			oldMetadata := make(map[string]job.FileMetadata)
			if err := getProjectDataMetadata(project, &oldMetadata); err != nil {
				return fmt.Errorf("retrieving data directory metadata: %v", err)
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
						return nil
					}
				}
				h := md5.New()
				if _, err := io.Copy(h, f); err != nil {
					return err
				}
				hStr := base64.StdEncoding.EncodeToString(h.Sum(nil))
				fileMd := job.FileMetadata{
					ModTime: mT,
					Hash:    hStr,
				}
				newMetadata[rP] = fileMd
				return nil
			}); err != nil {
				return fmt.Errorf("walking data directory %s: %v", dataDir, err)
			}

			if err := json.NewEncoder(bodyBuf).Encode(newMetadata); err != nil {
				return fmt.Errorf("encoding directory as json: %v", err)
			}
		} else {
			log.Printf("Data: no directory provided.\n")
		}

		b = bodyBuf.Bytes()
		if err := storeProjectDataMetadata(project, bytes.NewReader(b)); err != nil {
			return fmt.Errorf("storing data directory metadata: %v", err)
		}
		return nil
	}(); err != nil {
		log.Printf("Data: error: %v", err)
		errCh <- err
		return
	}

	m := "POST"
	h := "data.emrys.io"
	p := path.Join("user", uID, "project", project, "job", jID)
	u.Host = h
	u.Path = p

	uploadList := []string{}
	operation := func() error {
		req, err := http.NewRequest(m, u.String(), bytes.NewReader(b))
		if err != nil {
			return fmt.Errorf("creating request %v %v: %v", m, p, err)
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
		req = req.WithContext(ctx)

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode != http.StatusOK {
			b, _ := ioutil.ReadAll(resp.Body)
			return fmt.Errorf("server response: %s", b)
		}

		if err := json.NewDecoder(resp.Body).Decode(&uploadList); err != nil && err != io.EOF {
			return fmt.Errorf("decoding json response: %v", err)
		}
		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5), ctx),
		func(err error, t time.Duration) {
			log.Printf("Data: error: %v", err)
			log.Printf("Trying again in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Data: error: %v", err)
		errCh <- err
		return
	}

	log.Printf("Data: %d file(s) to upload\n", len(uploadList))

	if len(uploadList) > 0 {
		numUploaders := 5
		done := make(chan struct{})
		errCh := make(chan error, numUploaders)
		chUploadPaths := make(chan string, numUploaders)
		results := make(chan string, numUploaders)
		for i := 0; i < numUploaders; i++ {
			go uploadWorker(ctx, client, u, authToken, dataDir, done, errCh, chUploadPaths, results)
		}

		for _, relPath := range uploadList {
			chUploadPaths <- relPath
		}

		n := 0
	loop:
		for {
			select {
			case err := <-errCh:
				close(done)
				log.Printf("Data: error uploading data set: %v", err)
				errCh <- err
				return
			case result := <-results:
				log.Printf(result)
				n++
				if n == len(uploadList) {
					break loop
				}
			}
		}
	}

	log.Printf("Data: synced!\n")
}

func uploadWorker(ctx context.Context, client *http.Client, u url.URL, authToken, dataDir string, done <-chan struct{}, errCh chan<- error, upload <-chan string, results chan<- string) {
	m := "PUT"
	basePath := u.Path
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case relPath := <-upload:
			operation := func() error {
				log.Printf("Data: uploading: %v\n", relPath)

				uploadFilepath := path.Join(dataDir, relPath)
				f, err := os.Open(uploadFilepath)
				if err != nil {
					return fmt.Errorf("opening file %v: %v", uploadFilepath, err)
				}
				r, w := io.Pipe()
				zw := zlib.NewWriter(w)
				go func() {
					defer check.Err(w.Close)
					defer check.Err(zw.Close)
					defer check.Err(f.Close)
					if _, err := io.Copy(zw, f); err != nil {
						log.Printf("Data: error: copying file to zlib writer: %v", err)
						return
					}
				}()

				u.Path = path.Join(basePath, relPath)
				req, err := http.NewRequest(m, u.String(), r)
				if err != nil {
					return fmt.Errorf("creating request: %v", err)
				}
				req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
				req = req.WithContext(ctx)

				resp, err := client.Do(req)
				if err != nil {
					return err
				}
				defer check.Err(resp.Body.Close)

				if resp.StatusCode != http.StatusOK {
					b, _ := ioutil.ReadAll(resp.Body)
					return fmt.Errorf("server response: %s", b)
				}

				return nil
			}
			if err := backoff.RetryNotify(operation,
				backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5), ctx),
				func(err error, t time.Duration) {
					log.Printf("Data: error: %v", err)
					log.Printf("Trying again in %s seconds\n", t.Round(time.Second).String())
				}); err != nil {
				errCh <- err
				return
			}

			results <- fmt.Sprintf("Data: uploaded %s\n", relPath)
		}
	}
}
