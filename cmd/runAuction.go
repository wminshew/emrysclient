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
	req, err := http.NewRequest(m, u.String(), nil)
	if err != nil {
		return fmt.Errorf("creating request: %v", err)
	}
	req = req.WithContext(ctx)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	operation := func() error {
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode != http.StatusOK {
			b, _ := ioutil.ReadAll(resp.Body)
			return fmt.Errorf("server response: %s", b)
		}

		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5), ctx),
		func(err error, t time.Duration) {
			log.Printf("Auction error: %v", err)
			log.Printf("Trying again in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		return fmt.Errorf("%v", err)
	}

	log.Printf("Miner selected!\n")
	return nil
}
