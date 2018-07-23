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
)

func syncData(u url.URL, jID, authToken string, data []string) {
	fmt.Printf("Syncing data...\n")

	m := "POST"
	p := path.Join("data", jID)
	u.Path = p

	client := &http.Client{}
	var req *http.Request
	var resp *http.Response
	operation := func() error {
		var err error
		r, w := io.Pipe()
		go func() {
			if err := archiver.TarGz.Write(w, data); err != nil {
				fmt.Printf("Error tar-gzipping docker context files: %v\n", err)
				return
			}
		}()
		if req, err = http.NewRequest(m, u.String(), r); err != nil {
			fmt.Printf("Error creating request %v %v: %v\n", m, p, err)
			return err
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

		if resp, err = client.Do(req); err != nil {
			fmt.Printf("Error executing request %v %v: %v\n", m, p, err)
			return err
		}
		return nil
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
	fmt.Printf("Data synced!\n")
}
