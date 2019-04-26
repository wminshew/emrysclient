package job

import (
	"context"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/dustin/go-humanize"
	"github.com/pkg/errors"
	"github.com/shirou/gopsutil/disk"
	"github.com/wminshew/emrys/pkg/check"
	specs "github.com/wminshew/emrys/pkg/job"
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

// Job represents a user job
type Job struct {
	ID           string
	AuthToken    string
	Client       *http.Client
	Project      string
	Requirements string
	Main         string
	Notebook     bool
	SSHKey       []byte
	Data         string
	Output       string
	GPURaw       string
	RAMStr       string
	DiskStr      string
	PCIEStr      string
	Specs        *specs.Specs
}

const (
	pciePattern   = "^(16|8|4|2|1)x?$"
	maxRetries    = 10
	diskBufferStr = "5GB"
)

var (
	pcieRegexp = regexp.MustCompile(pciePattern)
)

// Send sends the job to the server
func (j *Job) Send(ctx context.Context, u url.URL) error {
	log.Printf("Sending requirements...\n")
	p := path.Join("user", "project", j.Project, "job")
	u.Path = p
	if j.Notebook {
		q := u.Query()
		q.Set("notebook", "1")
		u.RawQuery = q.Encode()
	}

	operation := func() error {
		req, err := http.NewRequest(http.MethodPost, u.String(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", j.AuthToken))

		resp, err := j.Client.Do(req)
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

		j.ID = resp.Header.Get("X-Job-ID")
		if j.Notebook {
			sshKeyBytes, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				return backoff.Permanent(fmt.Errorf("reading response: %v", err))
			}
			j.SSHKey = sshKeyBytes
		}
		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Error sending requirements: %v", err)
			log.Printf("Retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		return err
	}

	log.Printf("Beginning %s...\n", j.ID)
	return nil
}

// Cancel cancels the job with the server
func (j *Job) Cancel(u url.URL) error {
	log.Printf("Canceling...\n")

	ctx := context.Background()
	p := path.Join("user", "project", j.Project, "job", j.ID, "cancel")
	u.Path = p
	if j.Notebook {
		q := u.Query()
		q.Set("notebook", "1")
		u.RawQuery = q.Encode()
	}

	operation := func() error {
		req, err := http.NewRequest(http.MethodPost, u.String(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", j.AuthToken))
		req = req.WithContext(ctx)

		resp, err := j.Client.Do(req)
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

// ValidateAndTransform validates the job and transforms it into an appropriate server format
func (j *Job) ValidateAndTransform() error {
	if j.Project == "" {
		return fmt.Errorf("must specify a project in config or with flag")
	}
	projectRegexp := validate.ProjectRegexp()
	if !projectRegexp.MatchString(j.Project) {
		return fmt.Errorf("project (%s) must satisfy regex constraints: %s", j.Project, projectRegexp)
	}
	if j.Main == "" && j.Notebook == false {
		return fmt.Errorf("must specify a main execution file in config or with flag")
	} else if j.Notebook && j.Main != "" && filepath.Ext(j.Main) != ".ipynb" {
		return fmt.Errorf("with notebooks, must leave main blank or specify a .ipynb file in config or with flag")
	}
	if j.Requirements == "" {
		return fmt.Errorf("must specify a requirements file in config or with flag")
	}
	if j.Output == "" {
		return fmt.Errorf("must specify an output directory in config or with flag")
	}
	if j.Data == j.Output {
		return fmt.Errorf("can't use same directory for data and output")
	}
	if filepath.Base(j.Data) == "output" {
		return fmt.Errorf("can't name data directory \"output\"")
	}
	if j.Data != "" {
		if filepath.Dir(j.Main) != filepath.Dir(j.Data) {
			return fmt.Errorf("main (%v) and data (%v) must be in the same directory", j.Main, j.Data)
		}
	}
	if j.Main != "" && filepath.Dir(j.Main) != filepath.Dir(j.Output) {
		log.Printf("warning! Main (%v) will still only be able to save locally to "+
			"./output when executing, even though output (%v) has been set to a different "+
			"directory. Local output to ./output will be saved to your output (%v) at the end "+
			"of execution. If this is your intended workflow, please ignore this warning.\n",
			j.Main, j.Output, j.Output)
	}
	if j.Specs.Rate < 0 {
		return fmt.Errorf("can't use negative maximum rate")
	}
	var ok bool
	if j.Specs.GPU, ok = specs.ValidateGPU(j.GPURaw); !ok {
		return fmt.Errorf(`gpu not recognized. Please check https://docs.emrys.io/docs/suppliers/valid_gpus and 
			contact support@emrys.io if you think there has been a mistake.`)
	}
	var err error
	if j.Specs.RAM, err = humanize.ParseBytes(j.RAMStr); err != nil {
		return fmt.Errorf("error parsing ram: %v", err)
	}
	if j.Specs.Disk, err = humanize.ParseBytes(j.DiskStr); err != nil {
		return fmt.Errorf("error parsing disk: %v", err)
	}
	if j.Data != "" {
		diskBuffer, err := humanize.ParseBytes(diskBufferStr)
		if err != nil {
			return fmt.Errorf("error parsing disk buffer: %v", err)
		}

		diskUsage, err := disk.Usage(path.Join(filepath.Dir(j.Data), j.Data))
		if err != nil {
			return errors.Wrapf(err, "getting data set size")
		} else if diskUsage.Total > (j.Specs.Disk + diskBuffer) {
			return fmt.Errorf("insufficient requested disk space (data set: %s "+
				" + required disk buffer %s > requested disk space %s)", humanize.Bytes(j.Specs.Disk),
				humanize.Bytes(diskBuffer), humanize.Bytes(diskUsage.Total))
		}
	}
	pcieStr := pcieRegexp.FindStringSubmatch(j.PCIEStr)[1]
	if pcieStr == "" {
		return fmt.Errorf("error parsing pcie: please use a valid number of lanes followed " +
			"by an optional 'x' (i.e. 8, 8x, 16, 16x etc)")
	}
	if j.Specs.Pcie, err = strconv.Atoi(pcieStr); err != nil {
		return fmt.Errorf("error parsing pcie: please use a valid number of lanes followed " +
			"by an optional 'x' (i.e. 8, 8x, 16, 16x etc)")
	}
	return nil
}
