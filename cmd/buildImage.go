package cmd

import (
	"context"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/mholt/archiver"
	"github.com/wminshew/emrys/pkg/check"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"sync"
)

func buildImage(ctx context.Context, wg *sync.WaitGroup, errCh chan<- error, client *http.Client, u url.URL, uID, project, jID, authToken, main, reqs string) {
	defer wg.Done()
	m := "POST"
	p := path.Join("image", uID, project, jID)
	u.Path = p
	var req *http.Request
	var resp *http.Response
	operation := func() error {
		var err error
		r, w := io.Pipe()
		go func() {
			if err := archiver.TarGz.Write(w, []string{main, reqs}); err != nil {
				log.Printf("Image: error tar-gzipping docker context files: %v\n", err)
				return
			}
			if err := w.Close(); err != nil {
				log.Printf("Image: error closing pipe writer: %v\n", err)
				return
			}
		}()
		log.Printf("Image: packing request...\n")
		if req, err = http.NewRequest(m, u.String(), r); err != nil {
			log.Printf("Image: error creating request %v %v: %v\n", m, p, err)
			return err
		}
		req = req.WithContext(ctx)
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
		req.Header.Set("X-Main", filepath.Base(main))
		req.Header.Set("X-Reqs", filepath.Base(reqs))

		log.Printf("Image: building...\n")
		if resp, err = client.Do(req); err != nil {
			log.Printf("Image: error executing request %v %v: %v\n", m, p, err)
			return err
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode != http.StatusOK {
			log.Printf("Image: error %s %s\n", req.Method, req.URL.Path)
			log.Printf("Image: response header: %v\n", resp.Status)
			b, _ := ioutil.ReadAll(resp.Body)
			log.Printf("Image: response detail: %s", b)
			return fmt.Errorf("%s", b)
		}
		return nil
	}
	if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
		log.Printf("Image: error %v %v: %v\n", req.Method, req.URL.Path, err)
		errCh <- err
		return
	}
	log.Printf("Image: built!\n")
}
