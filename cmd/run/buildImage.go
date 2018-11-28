package run

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
	"time"
)

func buildImage(ctx context.Context, wg *sync.WaitGroup, errCh chan<- error, client *http.Client, u url.URL, uID, project, jID, authToken, main, reqs string) {
	defer wg.Done()
	m := "POST"
	p := path.Join("image", uID, project, jID)
	u.Path = p
	operation := func() error {
		log.Printf("Image: packing request...\n")
		r, w := io.Pipe()
		go func() {
			if err := archiver.TarGz.Write(w, []string{main, reqs}); err != nil {
				log.Printf("Image: error: tar-gzipping docker context files: %v", err)
				return
			}
			if err := w.Close(); err != nil {
				log.Printf("Image: error: closing pipe writer: %v", err)
				return
			}
		}()
		req, err := http.NewRequest(m, u.String(), r)
		if err != nil {
			return fmt.Errorf("creating %s %v: %v", req.Method, u, err)
		}
		req = req.WithContext(ctx)
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
		req.Header.Set("X-Main", filepath.Base(main))
		req.Header.Set("X-Reqs", filepath.Base(reqs))

		log.Printf("Image: building...\n")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode == http.StatusBadGateway {
			return fmt.Errorf("server: temporary error")
		} else if resp.StatusCode >= 300 {
			b, _ := ioutil.ReadAll(resp.Body)
			return backoff.Permanent(fmt.Errorf("server: %v", string(b)))
		}

		return nil
	}
	if err := backoff.RetryNotify(operation, backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxBackoffRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Image: error: %v", err)
			log.Printf("Retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Image: error: %v", err)
		errCh <- err
		return
	}
	log.Printf("Image: built!\n")
}
