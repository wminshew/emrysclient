package job

import (
	"bytes"
	"context"
	"encoding/json"
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

// RunAuction runs an auction on ths server for a job
func (j *Job) RunAuction(ctx context.Context, u url.URL) error {
	log.Printf("Searching for cheapest compute meeting your requirements...\n")
	p := path.Join("auction", j.ID)
	u.Path = p
	if j.Notebook {
		q := u.Query()
		q.Set("notebook", "1")
		u.RawQuery = q.Encode()
	}

	operation := func() error {
		bodyBuf := &bytes.Buffer{}
		if err := json.NewEncoder(bodyBuf).Encode(j.Specs); err != nil {
			return backoff.Permanent(err)
		}

		req, err := http.NewRequest(http.MethodPost, u.String(), bodyBuf)
		if err != nil {
			return err
		}
		req = req.WithContext(ctx)
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", j.AuthToken))

		resp, err := j.Client.Do(req)
		if err != nil {
			return err
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode == http.StatusPaymentRequired {
			return backoff.Permanent(fmt.Errorf("server: no compute meeting your requirements is available at this time"))
		} else if resp.StatusCode == http.StatusBadGateway {
			return fmt.Errorf("server: temporary error")
		} else if resp.StatusCode >= 300 {
			b, _ := ioutil.ReadAll(resp.Body)
			return backoff.Permanent(fmt.Errorf("server: %v", string(b)))
		}

		return nil
	}
	if err := backoff.RetryNotify(operation, backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Search: error: %v", err)
			log.Printf("Search: retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Search: error: %v", err)
		return err
	}

	log.Printf("Miner selected!\n")
	return nil
}
