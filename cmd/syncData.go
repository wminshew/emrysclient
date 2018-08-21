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
)

func syncData(ctx context.Context, wg *sync.WaitGroup, errCh chan<- error, client *http.Client, u url.URL, uID, project, jID, authToken string, dataDir string) {
	defer wg.Done()
	log.Printf("Data: syncing...\n")
	m := "POST"
	h := "data.emrys.io"
	p := path.Join("user", uID, "project", project, "job", jID)
	u.Host = h
	u.Path = p

	var req *http.Request
	var resp *http.Response
	bodyBuf := &bytes.Buffer{}
	operation := func() error {
		if dataDir != "" {
			oldMetadata := make(map[string]job.FileMetadata)
			if err := getProjectDataMetadata(project, &oldMetadata); err != nil {
				log.Printf("Data: error getting directory metadata: %v\n", err)
				return err
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
				log.Printf("Data: error walking directory %s: %v\n", dataDir, err)
				return err
			}

			if err := json.NewEncoder(bodyBuf).Encode(newMetadata); err != nil {
				log.Printf("Data: error encoding directory as JSON: %v\n", err)
				return err
			}
		} else {
			log.Printf("Data: no directory provided.\n")
		}

		// reqBodyBuf := &bytes.Buffer{}
		// tee := io.TeeReader(bodyBuf, reqBodyBuf)
		b := bodyBuf.Bytes()
		if err := storeProjectDataMetadata(project, bytes.NewReader(b)); err != nil {
			// if err := storeProjectDataMetadata(project, tee); err != nil {
			log.Printf("Data: error storing directory metadata: %v\n", err)
			return err
		}

		var err error
		if req, err = http.NewRequest(m, u.String(), bytes.NewReader(b)); err != nil {
			// if req, err = http.NewRequest(m, u.String(), reqBodyBuf); err != nil {
			log.Printf("Data: error creating request %v %v: %v\n", m, p, err)
			return err
		}
		req = req.WithContext(ctx)
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

		if resp, err = client.Do(req); err != nil {
			log.Printf("Data: error executing request %v %v: %v\n", m, p, err)
			return err
		}
		return nil
	}
	if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
		log.Printf("Data: error %v %v: %v\n", req.Method, req.URL.Path, err)
		errCh <- err
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("Data: error %s %s\n", req.Method, req.URL.Path)
		log.Printf("Data: response header: %v\n", resp.Status)
		b, _ := ioutil.ReadAll(resp.Body)
		log.Printf("Data: response detail: %s", b)
		check.Err(resp.Body.Close)
		errCh <- fmt.Errorf("%s", b)
		return
	}

	uploadList := []string{}
	if err := json.NewDecoder(resp.Body).Decode(&uploadList); err != nil && err != io.EOF {
		log.Printf("Data: failed to decode response body into string slice: %v\n", err)
		check.Err(resp.Body.Close)
		errCh <- err
		return
	}
	check.Err(resp.Body.Close)

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
				log.Printf("Data: error uploading data set: %v\n", err)
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
	basePath := u.Path
	for {
		select {
		case <-done:
			return
		case relPath := <-upload:
			var req *http.Request
			var resp *http.Response
			operation := func() error {
				var err error
				log.Printf("Data: uploading: %v\n", relPath)

				uploadFilepath := path.Join(dataDir, relPath)
				f, err := os.Open(uploadFilepath)
				if err != nil {
					log.Printf("Data: error opening file %v: %v\n", uploadFilepath, err)
					return err
				}
				r, w := io.Pipe()
				zw := zlib.NewWriter(w)
				go func() {
					defer check.Err(w.Close)
					defer check.Err(zw.Close)
					defer check.Err(f.Close)
					if _, err := io.Copy(zw, f); err != nil {
						log.Printf("Data: error copying file to zlib writer: %v\n", err)
						return
					}
				}()

				m := "PUT"
				u.Path = path.Join(basePath, relPath)
				if req, err = http.NewRequest(m, u.String(), r); err != nil {
					log.Printf("Data: error creating request %v %v: %v\n", m, u.Path, err)
					return err
				}
				req = req.WithContext(ctx)
				req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

				if resp, err = client.Do(req); err != nil {
					log.Printf("Data: error executing request %v %v: %v\n", m, u.Path, err)
					return err
				}
				return nil
			}
			if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
				log.Printf("Data: error %v %v: %v\n", req.Method, req.URL.Path, err)
				errCh <- err
				return
			}

			if resp.StatusCode != http.StatusOK {
				log.Printf("Data: error %s %s\n", req.Method, req.URL.Path)
				log.Printf("Data: response header: %v\n", resp.Status)
				b, _ := ioutil.ReadAll(resp.Body)
				log.Printf("Data: response detail: %s", b)
				errCh <- fmt.Errorf("Data: upload error %s", b)
				return
			}

			results <- fmt.Sprintf("Data: uploaded %s\n", relPath)
		}
	}
}
