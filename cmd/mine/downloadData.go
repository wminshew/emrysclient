package mine

import (
	"context"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/mholt/archiver"
	"github.com/wminshew/emrys/pkg/check"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path"
	"sync"
	"time"
)

func (w *worker) downloadData(ctx context.Context, wg *sync.WaitGroup, errCh chan<- error, u url.URL, jobDir string) {
	defer wg.Done()
	m := "GET"
	p := path.Join("miner", "job", w.jID)
	u.Host = "data.emrys.io"
	u.Path = p
	req, err := http.NewRequest(m, u.String(), nil)
	if err != nil {
		log.Printf("Data: error: creating request: %v", err)
		errCh <- err
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", *w.authToken))
	req = req.WithContext(ctx)

	log.Printf("Data: downloading...\n")
	operation := func() error {
		resp, err := w.client.Do(req)
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

		if resp.ContentLength != 0 {
			if err = archiver.TarGz.Read(resp.Body, jobDir); err != nil {
				return fmt.Errorf("unpacking response targz into temporary job directory %v: %v", jobDir, err)
			}
		}

		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Data: error: %v", err)
			log.Printf("Data: retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Data: error: %v", err)
		errCh <- err
		return
	}
	log.Printf("Data: downloaded!\n")
}
