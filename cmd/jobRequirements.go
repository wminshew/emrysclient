package cmd

import (
	"context"
	"errors"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/validate"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"time"
)

type jobReq struct {
	project      string
	requirements string
	main         string
	data         string
	output       string
}

func (j *jobReq) send(ctx context.Context, client *http.Client, u url.URL, uID, authToken string) (string, error) {
	log.Printf("Sending job requirements...\n")
	m := "POST"
	p := path.Join("user", uID, "project", j.project, "job")
	u.Path = p
	var jID string
	operation := func() error {
		req, err := http.NewRequest(m, u.String(), nil)
		if err != nil {
			return fmt.Errorf("creating request %v %v: %v", m, u, err)
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadGateway {
			b, _ := ioutil.ReadAll(resp.Body)
			return fmt.Errorf("server response: %s", b)
		} else if resp.StatusCode == http.StatusBadGateway {
			return fmt.Errorf("server response: temporary error")
		}

		jID = resp.Header.Get("X-Job-ID")
		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5), ctx),
		func(err error, t time.Duration) {
			log.Printf("Error sending job requirements: %v", err)
			log.Printf("Retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		return "", err
	}

	log.Printf("Job requirements sent!\n")
	return jID, nil
}

func (j *jobReq) cancel(client *http.Client, u url.URL, uID, jID, authToken string) error {
	log.Printf("Canceling job...\n")
	m := "POST"
	p := path.Join("user", uID, "project", j.project, "job", jID, "cancel")
	u.Path = p
	ctx := context.Background()
	operation := func() error {
		req, err := http.NewRequest(m, u.String(), nil)
		if err != nil {
			return fmt.Errorf("creating request %v %v: %v", m, u, err)
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
		req = req.WithContext(ctx)

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadGateway {
			b, _ := ioutil.ReadAll(resp.Body)
			return fmt.Errorf("server response: %s", b)
		} else if resp.StatusCode == http.StatusBadGateway {
			return fmt.Errorf("server response: temporary error")
		}

		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 10), ctx),
		func(err error, t time.Duration) {
			log.Printf("Error canceling job: %v", err)
			log.Printf("Retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		return err
	}

	log.Printf("Job canceled\n")
	return nil
}

func (j *jobReq) validate() error {
	if j.project == "" {
		return errors.New("must specify a project in config or with flag")
	}
	projectRegexp := validate.ProjectRegexp()
	if !projectRegexp.MatchString(j.project) {
		return fmt.Errorf("project (%s) must satisfy regex constraints: %s", j.project, projectRegexp)
	}
	if j.main == "" {
		return errors.New("must specify a main execution file in config or with flag")
	}
	if j.requirements == "" {
		return errors.New("must specify a requirements file in config or with flag")
	}
	if j.output == "" {
		return errors.New("must specify an output directory in config or with flag")
	}
	if j.data == j.output {
		return errors.New("can't use same directory for data and output")
	}
	if filepath.Base(j.data) == "output" {
		return errors.New("can't name data directory \"output\"")
	}
	if j.data != "" {
		if filepath.Dir(j.main) != filepath.Dir(j.data) {
			return fmt.Errorf("main (%v) and data (%v) must be in the same directory", j.main, j.data)
		}
	}
	if filepath.Dir(j.main) != filepath.Dir(j.output) {
		log.Printf("warning! Main (%v) will still only be able to save locally to "+
			"./output when executing, even though output (%v) has been set to a different "+
			"directory. Local output to ./output will be saved to your output (%v) at the end "+
			"of execution. If this is your intended workflow, please ignore this warning.\n",
			j.main, j.output, j.output)
	}
	return nil
}
