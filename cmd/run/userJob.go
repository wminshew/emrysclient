package run

import (
	"context"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/dustin/go-humanize"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/validate"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"time"
)

// TODO: move to emrys/pkg so server can re-use code
type userJob struct {
	id           string
	authToken    string
	client       *http.Client
	userID       string
	project      string
	requirements string
	main         string
	data         string
	output       string
	Rate         float64 `json:"rate,omitempty"`
	GPU          string  `json:"gpu,omitempty"`
	RAM          string  `json:"ram,omitempty"`
	Disk         string  `json:"disk,omitempty"`
	Pcie         string  `json:"pcie,omitempty"`
}

const (
	pciePattern = "^(16|8|4|2|1)x?$"
)

func (j *userJob) send(ctx context.Context, u url.URL) error {
	log.Printf("Sending job requirements...\n")
	p := path.Join("user", j.userID, "project", j.project, "job")
	u.Path = p
	operation := func() error {
		req, err := http.NewRequest(post, u.String(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", j.authToken))

		resp, err := j.client.Do(req)
		if err != nil {
			return err
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode == http.StatusBadGateway {
			return fmt.Errorf("server: temporary error")
		} else if resp.StatusCode >= 300 {
			b, _ := ioutil.ReadAll(resp.Body)
			return backoff.Permanent(fmt.Errorf("server: %v", string(b)))
		}

		j.id = resp.Header.Get("X-Job-ID")
		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxBackoffRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Error sending job requirements: %v", err)
			log.Printf("Retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		return err
	}

	log.Printf("Beginning job %s...\n", j.id)
	return nil
}

func (j *userJob) cancel(u url.URL) error {
	log.Printf("Canceling job...\n")
	p := path.Join("user", j.userID, "project", j.project, "job", j.id, "cancel")
	u.Path = p
	ctx := context.Background()
	operation := func() error {
		req, err := http.NewRequest(post, u.String(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", j.authToken))
		req = req.WithContext(ctx)

		resp, err := j.client.Do(req)
		if err != nil {
			return err
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode == http.StatusBadGateway {
			return fmt.Errorf("server: temporary error")
		} else if resp.StatusCode >= 300 {
			b, _ := ioutil.ReadAll(resp.Body)
			return backoff.Permanent(fmt.Errorf("server: %v", string(b)))
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

func (j *userJob) validate() error {
	if j.project == "" {
		return fmt.Errorf("must specify a project in config or with flag")
	}
	projectRegexp := validate.ProjectRegexp()
	if !projectRegexp.MatchString(j.project) {
		return fmt.Errorf("project (%s) must satisfy regex constraints: %s", j.project, projectRegexp)
	}
	if j.main == "" {
		return fmt.Errorf("must specify a main execution file in config or with flag")
	}
	if j.requirements == "" {
		return fmt.Errorf("must specify a requirements file in config or with flag")
	}
	if j.output == "" {
		return fmt.Errorf("must specify an output directory in config or with flag")
	}
	if j.data == j.output {
		return fmt.Errorf("can't use same directory for data and output")
	}
	if filepath.Base(j.data) == "output" {
		return fmt.Errorf("can't name data directory \"output\"")
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
	if j.Rate < 0 {
		return fmt.Errorf("can't use negative maximum job rate")
	}
	// TODO: validate gpu
	// if _, ok := emrys.AcceptedGPUs[j.GPU]; !ok {
	// 	return fmt.Errorf("minimum gpu not recognized. Please check documentation")
	// }
	if _, err := humanize.ParseBytes(j.RAM); err != nil {
		return fmt.Errorf("failed to parse ram: %v", err)
	}
	if _, err := humanize.ParseBytes(j.Disk); err != nil {
		return fmt.Errorf("failed to parse disk: %v", err)
	}
	pcieRegexp := regexp.MustCompile(pciePattern)
	if !pcieRegexp.MatchString(j.Pcie) {
		return fmt.Errorf("failed to parse pcie: please use a valid number of lanes followed " +
			"by an optional 'x' (i.e. 8, 8x, 16, 16x etc)")
	}
	return nil
}
