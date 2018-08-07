package cmd

import (
	"context"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/mholt/archiver"
	"github.com/wminshew/emrys/pkg/check"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
)

func buildImage(ctx context.Context, client *http.Client, u url.URL, project, jID, authToken, main, reqs string) {
	m := "POST"
	p := path.Join("image", project, jID)
	u.Path = p
	var req *http.Request
	var resp *http.Response
	operation := func() error {
		var err error
		r, w := io.Pipe()
		go func() {
			if err := archiver.TarGz.Write(w, []string{main, reqs}); err != nil {
				log.Printf("Error tar-gzipping docker context files: %v\n", err)
				return
			}
			if err := w.Close(); err != nil {
				log.Printf("Error closing pipe writer: %v\n", err)
				return
			}
		}()
		log.Printf("Packing image request...\n")
		if req, err = http.NewRequest(m, u.String(), r); err != nil {
			log.Printf("Error creating request %v %v: %v\n", m, p, err)
			return err
		}
		req = req.WithContext(ctx)
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
		req.Header.Set("X-Main", filepath.Base(main))
		req.Header.Set("X-Reqs", filepath.Base(reqs))

		log.Printf("Building image...\n")
		if resp, err = client.Do(req); err != nil {
			log.Printf("Error executing request %v %v: %v\n", m, p, err)
			return err
		}
		return nil
	}
	if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
		log.Printf("Error %v %v: %v\n", req.Method, req.URL.Path, err)
		return
	}
	defer check.Err(resp.Body.Close)

	if resp.StatusCode != http.StatusOK {
		log.Printf("Response error header: %v\n", resp.Status)
		b, _ := ioutil.ReadAll(resp.Body)
		log.Printf("Response error detail: %s\n", b)
		return
	}
	log.Printf("Image built!\n")
}
