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
	"strconv"
	"time"
)

func (w *worker) bid(ctx context.Context, client *http.Client, u url.URL, mID, authToken string, msg *job.Message) {
	bidsOut++
	defer func() { bidsOut-- }()
	u.RawQuery = ""
	jID := msg.Job.ID.String()
	dStr := strconv.Itoa(int(w.device))

	var body bytes.Buffer
	b := &job.Bid{
		BidRate:  w.bidRate,
		DeviceID: w.uuid,
	}
	if err := json.NewEncoder(&body).Encode(b); err != nil {
		log.Printf("Device %s: bid error: encoding json: %v", dStr, err)
		return
	}

	m := "POST"
	p := path.Join("miner", "job", jID, "bid")
	u.Path = p
	req, err := http.NewRequest(m, u.String(), &body)
	if err != nil {
		log.Printf("Device %s: bid error: creating request: %v", dStr, err)
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
	req = req.WithContext(ctx)

	log.Printf("Device %s: sending bid with rate: %v...\n", dStr, b.BidRate)
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
			log.Printf("Device %s: bid rejected -- you're busy with job %s!\n", dStr, w.jID)
		} else if resp.StatusCode == http.StatusOK {
			winner = true
		} else if resp.StatusCode == http.StatusPaymentRequired {
			log.Printf("Device %s: your bid was too low, maybe next time!\n", dStr)
		} else {
			b, _ := ioutil.ReadAll(resp.Body)
			return fmt.Errorf("server response: %s", b)
		}

		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5), ctx),
		func(err error, t time.Duration) {
			log.Printf("Device %s: bid error: %v", dStr, err)
			log.Printf("Device %s: trying again in %s seconds\n", dStr, t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Device %s: bid error: %v", dStr, err)
		return
	}

	if winner {
		log.Printf("Device %s: you won job %v!\n", dStr, jID)
		go w.executeJob(ctx, client, u, mID, authToken, jID)
	}
}
