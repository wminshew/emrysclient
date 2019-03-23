package worker

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

// Bid submits a bid on behalf of the Worker for a given job
func (w *Worker) Bid(ctx context.Context, u url.URL, msg *job.Message) {
	*w.BidsOut++
	defer func() { *w.BidsOut-- }()
	u.RawQuery = ""
	jID := msg.Job.ID.String()
	// TODO: pull notebook flag from msg.Job.Notebook
	dStr := strconv.Itoa(int(w.Device))

	b := &job.Bid{
		DeviceID: w.UUID,
		Specs: &job.Specs{
			Rate: w.BidRate,
			GPU:  w.gpu,
			RAM:  w.RAM,
			Disk: w.Disk,
			Pcie: w.pcie,
		},
	}

	log.Printf("Device %s: sending bid with rate: %v...\n", dStr, b.Specs.Rate)
	p := path.Join("miner", "job", jID, "bid")
	u.Path = p
	winner := false
	operation := func() error {
		body := &bytes.Buffer{}
		if err := json.NewEncoder(body).Encode(b); err != nil {
			return err
		}

		req, err := http.NewRequest(http.MethodPost, u.String(), body)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", *w.AuthToken))
		req = req.WithContext(ctx)

		resp, err := w.Client.Do(req)
		if err != nil {
			return err
		}
		defer check.Err(resp.Body.Close)

		if w.Busy {
			return backoff.Permanent(fmt.Errorf("already busy with job %s", w.JobID))
		} else if resp.StatusCode == http.StatusOK {
			winner = true
			sshKeyBytes, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				return backoff.Permanent(fmt.Errorf("reading response: %v", err))
			}
			if len(sshKeyBytes) > 0 {
				w.sshKey = sshKeyBytes
				w.notebook = true
			}
		} else if resp.StatusCode == http.StatusPaymentRequired {
			log.Printf("Device %s: bid not selected\n", dStr)
		} else if resp.StatusCode == http.StatusBadGateway {
			return fmt.Errorf("server: temporary error")
		} else if resp.StatusCode >= 300 {
			b, _ := ioutil.ReadAll(resp.Body)
			return backoff.Permanent(fmt.Errorf("server: %s", string(b)))
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
		w.JobID = jID
		go w.executeJob(ctx, u)
	}
}
