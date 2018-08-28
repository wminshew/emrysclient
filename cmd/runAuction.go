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

func runAuction(ctx context.Context, client *http.Client, u url.URL, jID, authToken string) (bool, error) {
	log.Printf("Running auction...\n")
	m := "POST"
	p := path.Join("auction", jID)
	u.Path = p
	req, err := http.NewRequest(m, u.String(), nil)
	if err != nil {
		return false, fmt.Errorf("creating request: %v", err)
	}
	req = req.WithContext(ctx)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	winner := false
	operation := func() error {
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("%s %v: %v", req.Method, u, err)
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode == http.StatusOK {
			winner = true
		} else if resp.StatusCode == http.StatusPaymentRequired {
			log.Println("No bids received, please try again")
		} else {
			b, _ := ioutil.ReadAll(resp.Body)
			return fmt.Errorf("server response: %s", b)
		}

		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5), ctx),
		func(err error, t time.Duration) {
			log.Printf("Auction error: %v\n", err)
			log.Printf("Trying again in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		return false, fmt.Errorf("%v", err)
	}

	if winner {
		log.Printf("Miner selected!\n")
	}
	return winner, nil
}
