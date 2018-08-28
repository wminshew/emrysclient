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
pollLoop:
	for {
		pr := pollResponse{}
		operation := func() error {
			req, err := http.NewRequest(m, u.String(), nil)
			if err != nil {
				return fmt.Errorf("creating request: %v", err)
			}
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

			resp, err := client.Do(req)
			if err != nil {
				return fmt.Errorf("%v %v: %v", req.Method, req.URL.Path, err)
			}
			defer check.Err(resp.Body.Close)

			if resp.StatusCode != http.StatusOK {
				b, _ := ioutil.ReadAll(resp.Body)
				return fmt.Errorf("server response: %s", b)
			}

			if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
				return fmt.Errorf("decoding json response: %v", err)
			}

			return nil
		}
		if err := backoff.RetryNotify(operation,
			backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5), ctx),
			func(err error, t time.Duration) {
				log.Printf("Output log: error: %v", err)
				log.Printf("Trying again in %s seconds\n", t.Round(time.Second).String())
			}); err != nil {
			return fmt.Errorf("%v", err)
		}

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
