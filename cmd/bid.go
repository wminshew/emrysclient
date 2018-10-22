package cmd

import (
	"bytes"
	"context"
	"docker.io/go-docker"
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

func (w *worker) bid(ctx context.Context, dClient *docker.Client, client *http.Client, u url.URL, mID, authToken, dockerAuthStr string, msg *job.Message) {
	bidsOut++
	defer func() { bidsOut-- }()
	u.RawQuery = ""
	jID := msg.Job.ID.String()
	dStr := strconv.Itoa(int(w.device))

	b := &job.Bid{
		BidRate:  w.bidRate,
		DeviceID: w.uuid,
	}

	log.Printf("Device %s: sending bid with rate: %v...\n", dStr, b.BidRate)
	m := "POST"
	p := path.Join("miner", "job", jID, "bid")
	u.Path = p
	winner := false
	operation := func() error {
		body := &bytes.Buffer{}
		if err := json.NewEncoder(body).Encode(b); err != nil {
			return err
		}

		req, err := http.NewRequest(m, u.String(), body)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
		req = req.WithContext(ctx)

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer check.Err(resp.Body.Close)

		if w.busy {
			log.Printf("Device %s: bid rejected -- you're busy with job %s!\n", dStr, w.jID)
		} else if resp.StatusCode == http.StatusOK {
			winner = true
		} else if resp.StatusCode == http.StatusPaymentRequired {
			log.Printf("Device %s: your bid was too high, maybe next time!\n", dStr)
		} else if resp.StatusCode == http.StatusBadGateway {
			return fmt.Errorf("server response: temporary error")
		} else {
			b, _ := ioutil.ReadAll(resp.Body)
			return fmt.Errorf("server response: %s", b)
		}

		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 3), ctx),
		func(err error, t time.Duration) {
			log.Printf("Device %s: bid error: %v", dStr, err)
			log.Printf("Device %s: retrying in %s seconds\n", dStr, t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Device %s: bid error: %v", dStr, err)
		return
	}

	if winner {
		log.Printf("Device %s: you won job %v!\n", dStr, jID)
		go w.executeJob(ctx, dClient, client, u, mID, jID, authToken, dockerAuthStr)
	}
}
