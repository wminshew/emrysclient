package cmd

import (
	"context"
	"docker.io/go-docker"
	"docker.io/go-docker/api/types"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/jsonmessage"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"sync"
	"time"
)

func downloadImage(ctx context.Context, wg *sync.WaitGroup, errCh chan<- error, client *http.Client, u url.URL, cli *docker.Client, refStr, jID, authToken string) {
	defer wg.Done()
	log.Printf("Image: downloading...\n")
	authConfig := types.AuthConfig{
		RegistryToken: authToken,
	}
	authJSON, err := json.Marshal(authConfig)
	if err != nil {
		log.Printf("Image: error: marshaling auth config into json: %v", err)
		errCh <- err
		return
	}
	authStr := base64.URLEncoding.EncodeToString(authJSON)
	operation := func() error {
		pullResp, err := cli.ImagePull(ctx, refStr, types.ImagePullOptions{
			RegistryAuth: authStr,
		})
		if err != nil {
			return err
		}
		defer check.Err(pullResp.Close)

		if err := jsonmessage.DisplayJSONMessagesStream(pullResp, os.Stdout, os.Stdout.Fd(), nil); err != nil {
			return err
		}

		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5), ctx),
		func(err error, t time.Duration) {
			log.Printf("Image: downloading error: %v", err)
			log.Printf("Image: trying again in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Image: downloading error: %v", err)
		errCh <- err
		return
	}

	m := "POST"
	p := path.Join("image", "downloaded", jID)
	u.Path = p
	req, err := http.NewRequest(m, u.String(), nil)
	if err != nil {
		log.Printf("Image: error: creating request: %v", err)
		errCh <- err
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
	req = req.WithContext(ctx)

	operation = func() error {
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode != http.StatusOK {
			b, _ := ioutil.ReadAll(resp.Body)
			return fmt.Errorf("server response: %s", b)
		}

		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5), ctx),
		func(err error, t time.Duration) {
			log.Printf("Image: error: %v", err)
			log.Printf("Image: trying again in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Image: error: %v", err)
		errCh <- err
		return
	}
}
