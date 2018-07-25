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
)

func syncData(ctx context.Context, client *http.Client, u url.URL, jID, authToken string, data []string) {
	log.Printf("Syncing data...\n")
	m := "POST"
	p := path.Join("data", jID)
	u.Path = p
	var req *http.Request
	var resp *http.Response
	operation := func() error {
		var err error
		r, w := io.Pipe()
		go func() {
			if err := archiver.TarGz.Write(w, data); err != nil {
				log.Printf("Error tar-gzipping docker context files: %v\n", err)
				return
			}
		}()
		if req, err = http.NewRequest(m, u.String(), r); err != nil {
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
	defer check.Err(resp.Body.Close)

	if resp.StatusCode != http.StatusOK {
		log.Printf("Response error header: %v\n", resp.Status)
		b, _ := ioutil.ReadAll(resp.Body)
		log.Printf("Response error detail: %s\n", b)
		return
	}
	log.Printf("Data synced!\n")
}
