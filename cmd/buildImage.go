package cmd

import (
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/mholt/archiver"
	"github.com/wminshew/emrys/pkg/check"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
)

func buildImage(u url.URL, jID, authToken, main, reqs string) {
	fmt.Printf("Building image...\n")

	r, w := io.Pipe()
	go func() {
		if err := archiver.TarGz.Write(w, []string{main, reqs}); err != nil {
			fmt.Printf("Error tar-gzipping docker context files: %v\n", err)
			return
		}
	}()

	m := "POST"
	p := path.Join("image", jID)
	u.Path = p
	req, err := http.NewRequest(m, u.String(), r)
	if err != nil {
		fmt.Printf("Error creating request %v %v: %v\n", m, p, err)
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
	req.Header.Set("X-Main", filepath.Base(main))
	req.Header.Set("X-Reqs", filepath.Base(reqs))

	client := &http.Client{}
	var resp *http.Response
	operation := func() error {
		var err error
		resp, err = client.Do(req)
		return err
	}
	expBackOff := backoff.NewExponentialBackOff()
	if err := backoff.Retry(operation, expBackOff); err != nil {
		fmt.Printf("Error %v %v: %v\n", req.Method, req.URL.Path, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Response error header: %v\n", resp.Status)
		b, _ := ioutil.ReadAll(resp.Body)
		fmt.Printf("Response error detail: %s\n", b)
		check.Err(resp.Body.Close)
		return
	}
	check.Err(resp.Body.Close)
	fmt.Printf("Image built!\n")
}
