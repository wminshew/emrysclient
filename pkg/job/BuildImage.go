package job

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

// BuildImage sends information to the server to build the image
func (j *Job) BuildImage(ctx context.Context, wg *sync.WaitGroup, errCh chan<- error, u url.URL) {
	defer wg.Done()
	p := path.Join("image", j.Project, j.ID)
	u.Path = p
	if j.Notebook {
		q := u.Query()
		q.Set("notebook", "1")
		u.RawQuery = q.Encode()
	}

	operation := func() error {
		log.Printf("Image: packing request...\n")
		r, w := io.Pipe()
		go func() {
			dockerContext := []string{j.Requirements}
			if !j.Notebook || j.Main != "" {
				dockerContext = append(dockerContext, j.Main)
			}
			if err := archiver.TarGz.Write(w, dockerContext); err != nil {
				log.Printf("Image: error: tar-gzipping docker context files: %v", err)
				return
			}
			if err := w.Close(); err != nil {
				log.Printf("Image: error: closing pipe writer: %v", err)
				return
			}
		}()
		req, err := http.NewRequest(http.MethodPost, u.String(), r)
		if err != nil {
			return err
		}
		req = req.WithContext(ctx)
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", j.AuthToken))
		req.Header.Set("X-Main", filepath.Base(j.Main))
		req.Header.Set("X-Reqs", filepath.Base(j.Requirements))

		log.Printf("Image: building...\n")
		resp, err := j.Client.Do(req)
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
	if err := backoff.RetryNotify(operation, backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries), ctx),
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
