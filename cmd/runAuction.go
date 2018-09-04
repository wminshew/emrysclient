package cmd

import (
	"context"
	"fmt"
	"github.com/wminshew/emrys/pkg/check"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path"
)

func runAuction(ctx context.Context, client *http.Client, u url.URL, jID, authToken string) error {
	log.Printf("Running auction...\n")
	m := "POST"
	p := path.Join("auction", jID)
	u.Path = p
	req, err := http.NewRequest(m, u.String(), nil)
	if err != nil {
		return fmt.Errorf("creating request: %v", err)
	}
	req = req.WithContext(ctx)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer check.Err(resp.Body.Close)

	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("server response: %s", b)
	}

	log.Printf("Miner selected!\n")
	return nil
}
