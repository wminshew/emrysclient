package run

import (
	"context"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/mholt/archiver"
	"github.com/wminshew/emrys/pkg/check"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"time"
)

func (j *userJob) downloadOutputData(ctx context.Context, u url.URL) error {
	log.Printf("Output data: downloading...\n")

	outputDir := filepath.Join(j.output, j.id, "data")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("making output directory %v: %v", outputDir, err)
	}

	p := path.Join("job", j.id, "data")
	u.Path = p
	operation := func() error {
		req, err := http.NewRequest(get, u.String(), nil)
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
		} else if resp.StatusCode == http.StatusNoContent {
			return fmt.Errorf("server: output data not yet uploaded")
		}

		if err = archiver.TarGz.Read(resp.Body, outputDir); err != nil {
			return fmt.Errorf("unpacking .tar.gz into output directory %v: %v", outputDir, err)
		}

		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Output data: error: %v", err)
			log.Printf("Retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		return fmt.Errorf("%s", err)
	}

	log.Printf("Output data: downloaded!\n")
	return nil
}
