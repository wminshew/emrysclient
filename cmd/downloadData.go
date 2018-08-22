package cmd

import (
	"context"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/mholt/archiver"
	"github.com/wminshew/emrys/pkg/check"
	// "io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path"
	"sync"
)

func downloadData(ctx context.Context, wg *sync.WaitGroup, errCh chan<- error, client *http.Client, u url.URL, jID, authToken, jobDir string) {
	defer wg.Done()
	m := "GET"
	p := path.Join("miner", "job", jID)
	u.Host = "data.emrys.io"
	u.Path = p
	req, err := http.NewRequest(m, u.String(), nil)
	if err != nil {
		log.Printf("Data: failed to create http request %v %v: %v\n", m, p, err)
		errCh <- err
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	log.Printf("Data: downloading...\n")
	var resp *http.Response
	operation := func() error {
		var err error
		if resp, err = client.Do(req); err != nil {
			return err
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode != http.StatusOK {
			log.Printf("Data: error %v %v\n", m, p)
			log.Printf("Data: response header: %v\n", resp.Status)
			b, _ := ioutil.ReadAll(resp.Body)
			log.Printf("Data: response detail: %s", b)
			return fmt.Errorf("%v", b)
		}

		if resp.ContentLength != 0 {
			if err = archiver.TarGz.Read(resp.Body, jobDir); err != nil {
				log.Printf("Data: error unpacking .tar.gz into job dir %v: %v\n", jobDir, err)
				return err
			}
		}

		return nil
	}
	if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
		log.Printf("Data: error %v %v: %v\n", req.Method, req.URL.Path, err)
		errCh <- err
		return
	}
	log.Printf("Data: downloaded!\n")
}
