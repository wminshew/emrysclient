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
	"time"
)

func runAuction(ctx context.Context, client *http.Client, u url.URL, jID, authToken string) error {
	log.Printf("Running auction...\n")
	m := "POST"
	p := path.Join("auction", jID)
	u.Path = p
	operation := func() error {
		req, err := http.NewRequest(m, u.String(), nil)
		if err != nil {
			return fmt.Errorf("creating request: %v", err)
		}
		req = req.WithContext(ctx)
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

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
			log.Printf("Auction: error: %v", err)
			log.Printf("Auction: retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Auction: error: %v", err)
		return err
	}

	log.Printf("Miner selected!\n")
	return nil
}
