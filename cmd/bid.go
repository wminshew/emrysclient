package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/job"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path"
	"time"
)

func (w *worker) bid(ctx context.Context, client *http.Client, u url.URL, mID, authToken string, msg *job.Message) {
	bidsOut++
	defer func() { bidsOut-- }()
	u.RawQuery = ""
	jID := msg.Job.ID.String()

	var body bytes.Buffer
	b := &job.Bid{
		BidRate:  w.bidRate,
		DeviceID: w.uuid,
	}
	if err := json.NewEncoder(&body).Encode(b); err != nil {
		log.Printf("Bid error: encoding json: %v", err)
		return
	}

	m := "POST"
	p := path.Join("miner", "job", jID, "bid")
	u.Path = p
	req, err := http.NewRequest(m, u.String(), &body)
	if err != nil {
		log.Printf("Bid error: creating request: %v", err)
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
	req = req.WithContext(ctx)

	log.Printf("Sending bid with rate: %v...\n", b.BidRate)
	var resp *http.Response
	winner := false
	operation := func() error {
		var err error
		resp, err = client.Do(req)
		if err != nil {
			return fmt.Errorf("%s %v: %v", req.Method, req.URL.Path, err)
		}
		defer check.Err(resp.Body.Close)

		if w.busy {
			log.Printf("Bid rejected -- you're busy with job %s!\n", w.jID)
		} else if resp.StatusCode == http.StatusOK {
			winner = true
		} else if resp.StatusCode == http.StatusPaymentRequired {
			log.Println("Your bid was too low, maybe next time!")
		} else {
			b, _ := ioutil.ReadAll(resp.Body)
			return fmt.Errorf("server response: %s", b)
		}

		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5), ctx),
		func(err error, t time.Duration) {
			log.Printf("Bid error: %v", err)
			log.Printf("Trying again in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Bid error: %v", err)
		return
	}

	if winner {
		log.Printf("You won job %v!\n", jID)
		go w.executeJob(ctx, client, u, mID, authToken, jID)
	}
}
