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
)

func downloadOutputData(ctx context.Context, client *http.Client, u url.URL, jID, authToken, output string) error {
	log.Printf("Output data: downloading...\n")

	m := "GET"
	p := path.Join("job", jID, "data")
	u.Path = p
	var req *http.Request
	var resp *http.Response
	operation := func() error {
		var err error
		if req, err = http.NewRequest(m, u.String(), nil); err != nil {
			log.Printf("Output data: error creating request %v %v: %v\n", m, p, err)
			return err
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

		if resp, err = client.Do(req); err != nil {
			log.Printf("Output data: error %v %v: %v\n", req.Method, req.URL.Path, err)
			return err
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode != http.StatusOK {
			log.Printf("Output data: error %s %s\n", req.Method, req.URL.Path)
			log.Printf("Output data: response header: %v\n", resp.Status)
			b, _ := ioutil.ReadAll(resp.Body)
			log.Printf("Output data: response detail: %s", b)
			return fmt.Errorf("Output data: response error: %s", b)
		}

		outputDir := filepath.Join(output, jID, "data")
		if err = os.MkdirAll(outputDir, 0755); err != nil {
			log.Printf("Output data: error making output dir %v: %v\n", outputDir, err)
			return err
		}
		if err = archiver.TarGz.Read(resp.Body, outputDir); err != nil {
			log.Printf("Output data: error unpacking .tar.gz into output dir %v: %v\n", outputDir, err)
			return err
		}

		return nil
	}
	if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
		log.Printf("Output data: error %v %v: %v\n", req.Method, req.URL.Path, err)
		return err
	}
	return nil
}
