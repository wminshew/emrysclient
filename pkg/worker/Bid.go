package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/pkg/errors"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/mem"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/job"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path"
	"time"
)

// Bid submits a bid on behalf of the Worker for a given job
func (w *Worker) Bid(ctx context.Context, u url.URL, msg *job.Message) error {
	*w.BidsOut++
	defer func() { *w.BidsOut-- }()
	u.RawQuery = ""
	jID := msg.Job.ID.String()

	b := &job.Bid{
		DeviceID: w.Snapshot.ID,
		Specs: &job.Specs{
			Rate: w.BidRate,
			GPU:  w.Snapshot.Name,
			RAM:  w.RAM,
			Disk: w.Disk,
			Pcie: int(w.Snapshot.PcieMaxWidth),
		},
	}

	// TODO: account for mem reserved for other in-process jobs
	// have to check for busy workers (w.Busy), add reserved ram (w.RAM), and then delete already-allocated-to-job RAM [via docker]
	memStats, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return errors.Wrapf(err, "device %d: getting memory stats", w.Device)
	} else if w.RAM > memStats.Free {
		return fmt.Errorf("device %d: insufficient available memory (requested for bidding: %d "+
			"> system memory available %d)", w.Device, w.RAM, memStats.Free)
	}

	// TODO: account for disk reserved for other in-process jobs
	// have to check for busy workers (w.Busy), add reserved disk (w.Disk), and then delete already-allocated-to-job Disk [via gopsutil, docker]
	diskUsage, err := disk.UsageWithContext(ctx, "/")
	if err != nil {
		return errors.Wrapf(err, "getting disk usage")
	} else if w.Disk > diskUsage.Free {
		return fmt.Errorf("insufficient available disk space (requested for bidding: %d "+
			"> system disk space available %d)", w.Disk, diskUsage.Free)
	}

	log.Printf("Mine: bid: device %d: sending bid with rate: %v...\n", w.Device, b.Specs.Rate)
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
			log.Printf("Mine: bid: device %d: bid not selected\n", w.Device)
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
			log.Printf("Mine: bid: device %d: bid error: %v. Retrying in %s seconds\n", w.Device, err, t.Round(time.Second).String())
		}); err != nil {
		return errors.Wrapf(err, "device %d: sending bid to server", w.Device)
	}

	if winner {
		log.Printf("Mine: bid: device %d: you won job %v!\n", w.Device, jID)
		go w.executeJob(ctx, u, jID)
	}

	return nil
}
