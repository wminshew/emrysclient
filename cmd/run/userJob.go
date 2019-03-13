package run

import (
	"context"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/dustin/go-humanize"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/job"
	"github.com/wminshew/emrys/pkg/validate"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

type userJob struct {
	id           string
	authToken    string
	client       *http.Client
	project      string
	requirements string
	main         string
	notebook     bool
	sshKey       []byte
	data         string
	output       string
	gpuRaw       string
	ramStr       string
	diskStr      string
	pcieStr      string
	specs        *job.Specs
}

const (
	pciePattern = "^(16|8|4|2|1)x?$"
	maxRetries  = 10
)

var (
	pcieRegexp = regexp.MustCompile(pciePattern)
)

func (j *userJob) send(ctx context.Context, u url.URL) error {
	log.Printf("Sending requirements...\n")
	p := path.Join("user", "project", j.project, "job")
	u.Path = p
	if j.notebook {
		q := u.Query()
		q.Set("notebook", "1")
		u.RawQuery = q.Encode()
	}

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
		if j.notebook {
			sshKeyBytes, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				return backoff.Permanent(fmt.Errorf("reading response: %v", err))
			}
			j.sshKey = sshKeyBytes
		}
		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxBackoffRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Error sending requirements: %v", err)
			log.Printf("Retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		return err
	}

	log.Printf("Beginning %s...\n", j.id)
	return nil
}

func (j *userJob) cancel(u url.URL) error {
	log.Printf("Canceling...\n")

	p := path.Join("job", j.id, "cancel")
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
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Error canceling: %v", err)
			log.Printf("Retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		return err
	}

	p = path.Join("user", "project", j.project, "job", j.id, "cancel")
	u.Path = p
	if j.notebook {
		q := u.Query()
		q.Set("notebook", "1")
		u.RawQuery = q.Encode()
	}

	operation = func() error {
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
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Error canceling: %v", err)
			log.Printf("Retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		return err
	}

	log.Printf("Job canceled\n")
	return nil
}

func (j *userJob) validateAndTransform() error {
	if j.project == "" {
		return fmt.Errorf("must specify a project in config or with flag")
	}
	projectRegexp := validate.ProjectRegexp()
	if !projectRegexp.MatchString(j.project) {
		return fmt.Errorf("project (%s) must satisfy regex constraints: %s", j.project, projectRegexp)
	}
	if j.main == "" && j.notebook == false {
		return fmt.Errorf("must specify a main execution file in config or with flag")
	} else if j.notebook && j.main != "" && filepath.Ext(j.main) != ".ipynb" {
		return fmt.Errorf("must leave main blank or use a .ipynb file in config or with flag with notebooks")
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
	if j.main != "" && filepath.Dir(j.main) != filepath.Dir(j.output) {
		log.Printf("warning! Main (%v) will still only be able to save locally to "+
			"./output when executing, even though output (%v) has been set to a different "+
			"directory. Local output to ./output will be saved to your output (%v) at the end "+
			"of execution. If this is your intended workflow, please ignore this warning.\n",
			j.main, j.output, j.output)
	}
	if j.specs.Rate < 0 {
		return fmt.Errorf("can't use negative maximum rate")
	}
	var ok bool
	if j.specs.GPU, ok = job.ValidateGPU(j.gpuRaw); !ok {
		return fmt.Errorf("gpu not recognized. Please check documentation")
	}
	var err error
	if j.specs.RAM, err = humanize.ParseBytes(j.ramStr); err != nil {
		return fmt.Errorf("error parsing ram: %v", err)
	}
	if j.specs.Disk, err = humanize.ParseBytes(j.diskStr); err != nil {
		return fmt.Errorf("error parsing disk: %v", err)
	}
	pcieStr := pcieRegexp.FindStringSubmatch(j.pcieStr)[1]
	if pcieStr == "" {
		return fmt.Errorf("error parsing pcie: please use a valid number of lanes followed " +
			"by an optional 'x' (i.e. 8, 8x, 16, 16x etc)")
	}
	if j.specs.Pcie, err = strconv.Atoi(pcieStr); err != nil {
		return fmt.Errorf("error parsing pcie: please use a valid number of lanes followed " +
			"by an optional 'x' (i.e. 8, 8x, 16, 16x etc)")
	}
	return nil
}
