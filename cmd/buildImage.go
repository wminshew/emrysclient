package cmd

import (
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

func buildImage(client *http.Client, u url.URL, jID, authToken, main, reqs string) {
	log.Printf("Building image...\n")
	m := "POST"
	p := path.Join("image", jID)
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
		}()
		if req, err = http.NewRequest(m, u.String(), r); err != nil {
			log.Printf("Error creating request %v %v: %v\n", m, p, err)
			return err
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
		req.Header.Set("X-Main", filepath.Base(main))
		req.Header.Set("X-Reqs", filepath.Base(reqs))

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
