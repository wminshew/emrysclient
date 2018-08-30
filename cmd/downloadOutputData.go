package cmd

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

func downloadOutputData(ctx context.Context, client *http.Client, u url.URL, jID, authToken, output string) error {
	log.Printf("Output data: downloading...\n")

	outputDir := filepath.Join(output, jID, "data")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("making output directory %v: %v", outputDir, err)
	}

	m := "GET"
	p := path.Join("job", jID, "data")
	u.Path = p
	operation := func() error {
		req, err := http.NewRequest(m, u.String(), nil)
		if err != nil {
			return fmt.Errorf("creating request: %v", err)
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
		req = req.WithContext(ctx)

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("%v %v: %v", req.Method, req.URL.Path, err)
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode != http.StatusOK {
			b, _ := ioutil.ReadAll(resp.Body)
			return fmt.Errorf("server response: %s", b)
		}

		if err = archiver.TarGz.Read(resp.Body, outputDir); err != nil {
			return fmt.Errorf("unpacking .tar.gz into output directory %v: %v", outputDir, err)
		}

		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5), ctx),
		func(err error, t time.Duration) {
			log.Printf("Output data: error: %v\n", err)
			log.Printf("Trying again in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		return fmt.Errorf("%s", err)
	}
	return nil
}
