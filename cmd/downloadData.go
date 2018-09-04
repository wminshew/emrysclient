package cmd

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

func downloadData(ctx context.Context, wg *sync.WaitGroup, errCh chan<- error, client *http.Client, u url.URL, jID, authToken, jobDir string) {
	defer wg.Done()
	m := "GET"
	p := path.Join("miner", "job", jID)
	u.Host = "data.emrys.io"
	u.Path = p
	req, err := http.NewRequest(m, u.String(), nil)
	if err != nil {
		log.Printf("Data: error: creating request: %v", err)
		errCh <- err
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
	req = req.WithContext(ctx)

	log.Printf("Data: downloading...\n")
	var resp *http.Response
	operation := func() error {
		var err error
		if resp, err = client.Do(req); err != nil {
			return fmt.Errorf("%s %s: %v", req.Method, req.URL, err)
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode != http.StatusOK {
			b, _ := ioutil.ReadAll(resp.Body)
			return fmt.Errorf("%v detail: %s", resp.StatusCode, b)
		}

		if resp.ContentLength != 0 {
			if err = archiver.TarGz.Read(resp.Body, jobDir); err != nil {
				return fmt.Errorf("unpacking response targz into temporary job directory %v: %v", jobDir, err)
			}
		}

		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5), ctx),
		func(err error, t time.Duration) {
			log.Printf("Data: error: %v", err)
			log.Printf("Data: trying again in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Data: error %v %v: %v\n", req.Method, req.URL.Path, err)
		errCh <- err
		return
	}
	log.Printf("Data: downloaded!\n")
}
