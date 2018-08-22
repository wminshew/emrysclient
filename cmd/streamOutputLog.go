package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/wminshew/emrys/pkg/check"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"time"
)

type pollResponse struct {
	Events    []pollEvent `json:"events"`
	Timestamp int64       `json:"timestamp"`
}

// source: https://github.com/jcuga/golongpoll/blob/master/go-client/glpclient/client.go
type pollEvent struct {
	// Timestamp is milliseconds since epoch to match javascrits Date.getTime()
	Timestamp int64  `json:"timestamp"`
	Category  string `json:"category"`
	// Data can be anything that is able to passed to json.Marshal()
	Data json.RawMessage `json:"data"`
}

var maxTimeout = 60 * 2

func streamOutputLog(ctx context.Context, client *http.Client, u url.URL, jID, authToken, output string) error {
	log.Printf("Output log: streaming... (may take a minute to begin)\n")

	outputLogPath := filepath.Join(output, jID, "log")
	f, err := os.OpenFile(outputLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Output log: error creating output log file %v: %v\n", outputLogPath, err)
		return err
	}
	defer check.Err(f.Close)

	m := "GET"
	p := path.Join("job", jID, "log")
	u.Path = p
	q := u.Query()
	q.Set("timeout", fmt.Sprintf("%d", maxTimeout))
	buffer := int64(10)
	sinceTime := (time.Now().Unix() - buffer) * 1000
	q.Set("since_time", fmt.Sprintf("%d", sinceTime))
	u.RawQuery = q.Encode()
	var req *http.Request
	var resp *http.Response
	var operation backoff.Operation
pollLoop:
	for {
		operation = func() error {
			if req, err = http.NewRequest(m, u.String(), nil); err != nil {
				log.Printf("Output log: error creating request %v %v: %v\n", m, p, err)
				return err
			}
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

			if resp, err = client.Do(req); err != nil {
				log.Printf("Output log: error %v %v: %v\n", req.Method, req.URL.Path, err)
				return err
			}

			if resp.StatusCode != http.StatusOK {
				log.Printf("Output log: error %s %s\n", req.Method, req.URL.Path)
				log.Printf("Output log: response header: %v\n", resp.Status)
				b, _ := ioutil.ReadAll(resp.Body)
				log.Printf("Output log: response detail: %s", b)
				check.Err(resp.Body.Close)
				return fmt.Errorf("Output log: response error: %s", b)
			}
			return nil
		}
		if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
			log.Printf("Output log: error %v %v: %v\n", req.Method, req.URL.Path, err)
			return err
		}

		pr := pollResponse{}
		if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
			log.Printf("Error decoding json pollResponse: %v\n", err)
			check.Err(resp.Body.Close)
			return err
		}
		check.Err(resp.Body.Close)

		if len(pr.Events) > 0 {
			for _, event := range pr.Events {
				sinceTime = event.Timestamp
				var buf []byte
				if err := json.Unmarshal(event.Data, &buf); err != nil {
					var fin struct{}
					if err := json.Unmarshal(event.Data, &fin); err == nil {
						if fin == struct{}{} {
							break pollLoop
						}
					}
					log.Printf("Error unmarshaling json message: %v\n", err)
					log.Printf("json message: %s\n", string(event.Data))
					return err
				}

				tee := io.TeeReader(bytes.NewReader(buf), os.Stdout)
				if _, err = io.Copy(f, tee); err != nil {
					log.Printf("Output log: error copying response body: %v\n", err)
					return err
				}

			}
		} else {
			if pr.Timestamp > sinceTime {
				sinceTime = pr.Timestamp
			}
		}

		q = u.Query()
		q.Set("since_time", fmt.Sprintf("%d", sinceTime))
		u.RawQuery = q.Encode()
	}
	return nil
}
