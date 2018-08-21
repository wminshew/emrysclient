package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/spf13/viper"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/job"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path"
)

func bid(client *http.Client, u url.URL, mID, authToken string, msg *job.Message) {
	u.RawQuery = ""
	if err := checkVersion(client, u); err != nil {
		log.Printf("Version error: %v\n", err)
		return
	}
	jID := msg.Job.ID.String()

	var body bytes.Buffer
	b := &job.Bid{
		MinRate: viper.GetFloat64("bid-rate"),
	}
	if err := json.NewEncoder(&body).Encode(b); err != nil {
		log.Printf("Error encoding json bid: %v\n", err)
		return
	}
	m := "POST"
	p := path.Join("miner", "job", jID, "bid")
	u.Path = p
	req, err := http.NewRequest(m, u.String(), &body)
	if err != nil {
		log.Printf("Failed to create http request %v %v: %v\n", m, p, err)
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	log.Printf("Sending bid with rate: %v...\n", b.MinRate)
	var resp *http.Response
	operation := func() error {
		var err error
		resp, err = client.Do(req)
		return err
	}
	if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
		log.Printf("Error %v %v: %v\n", req.Method, req.URL.Path, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		if busy {
			log.Println("Bid rejected -- you're busy with another job!")
		} else if !busy && resp.StatusCode == http.StatusPaymentRequired {
			log.Println("Your bid was too low, maybe next time!")
		} else {
			log.Printf("http request error %v %v\n", m, p)
			log.Printf("Response header: %v\n", resp.Status)
			b, _ := ioutil.ReadAll(resp.Body)
			log.Printf("Response detail: %s", b)
			check.Err(resp.Body.Close)
		}
		return
	}
	check.Err(resp.Body.Close)
	log.Printf("You won job %v!\n", jID)
	executeJob(client, u, mID, authToken, jID)
}
