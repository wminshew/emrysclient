package cmd

import (
	"context"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/wminshew/emrys/pkg/check"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path"
)

func runAuction(ctx context.Context, client *http.Client, u url.URL, jID, authToken string) error {
	log.Printf("Running auction...\n")
	m := "POST"
	p := path.Join("auction", jID)
	u.Path = p
	req, err := http.NewRequest(m, u.String(), nil)
	if err != nil {
		log.Printf("Error creating request %v %v: %v\n", m, p, err)
		return err
	}
	req = req.WithContext(ctx)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	var resp *http.Response
	operation := func() error {
		var err error
		resp, err = client.Do(req)
		return err
	}
	if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
		log.Printf("Error %v %v: %v\n", req.Method, req.URL.Path, err)
		return err
	}
	defer check.Err(resp.Body.Close)

	if resp.StatusCode != http.StatusOK {
		log.Printf("Failed %s %s\n", req.Method, req.URL.Path)
		log.Printf("Response error header: %v\n", resp.Status)
		b, _ := ioutil.ReadAll(resp.Body)
		// log.Printf("Response error detail: %s", b)
		return fmt.Errorf("%s", b)
	}
	log.Printf("Miner selected!\n")
	return nil
}
