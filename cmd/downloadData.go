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
)

func downloadData(ctx context.Context, wg *sync.WaitGroup, errCh chan<- error, client *http.Client, u url.URL, jID, authToken, jobDir string) {
	defer wg.Done()
	m := "GET"
	p := path.Join("miner", "job", jID)
	u.Host = "data.emrys.io"
	u.Path = p
	req, err := http.NewRequest(m, u.String(), nil)
	if err != nil {
		log.Printf("Failed to create http request %v %v: %v\n", m, p, err)
		errCh <- err
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	log.Printf("Downloading data...\n")
	var resp *http.Response
	operation := func() error {
		var err error
		if resp, err = client.Do(req); err != nil {
			return err
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("http request error %v %v\n", m, p)
			log.Printf("Response error header: %v\n", resp.Status)
			b, _ := ioutil.ReadAll(resp.Body)
			log.Printf("Response error detail: %s", b)
			check.Err(resp.Body.Close)
			return fmt.Errorf("%v", b)
		}

		return nil
	}
	if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
		log.Printf("Error %v %v: %v\n", req.Method, req.URL.Path, err)
		errCh <- err
		return
	}
	defer check.Err(resp.Body.Close)

	if err = archiver.TarGz.Read(resp.Body, jobDir); err != nil {
		log.Printf("Error unpacking .tar.gz into job dir %v: %v\n", jobDir, err)
		errCh <- err
		return
	}
	log.Printf("Data downloaded!\n")
}
