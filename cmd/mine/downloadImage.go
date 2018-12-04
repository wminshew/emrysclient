package mine

import (
	"context"
	"docker.io/go-docker"
	"docker.io/go-docker/api/types"
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
	"strconv"
	"strings"
	"sync"
	"time"
)

func downloadImage(ctx context.Context, wg *sync.WaitGroup, errCh chan<- error, client *http.Client, u url.URL, dClient *docker.Client, refStr, jID, authToken, dockerAuthStr string) {
	defer wg.Done()
	log.Printf("Image: downloading...\n")
	operation := func() error {
		pullResp, err := dClient.ImagePull(ctx, refStr, types.ImagePullOptions{
			RegistryAuth: dockerAuthStr,
		})
		if err != nil {
			return err
		}
		defer check.Err(pullResp.Close)

		if err := jsonmessage.DisplayJSONMessagesStream(pullResp, os.Stdout, os.Stdout.Fd(), nil); err != nil {
			split := strings.Split(err.Error(), "unexpected HTTP status:")
			if len(split) == 1 {
				return backoff.Permanent(err)
			}
			trimmedStatus := strings.TrimSpace(split[1])
			statusCodeStr := trimmedStatus[:3]
			statusCode, _ := strconv.Atoi(statusCodeStr)

			if statusCode == http.StatusBadGateway {
				return fmt.Errorf("server: temporary error")
			} else if statusCode >= 300 {
				return backoff.Permanent(fmt.Errorf("server: %s", trimmedStatus[3:]))
			}

			return backoff.Permanent(err)
		}

		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxBackoffRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Image: downloading error: %v", err)
			log.Printf("Image: retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Image: downloading error: %v", err)
		errCh <- err
		return
	}

	p := path.Join("image", "downloaded", jID)
	u.Path = p
	req, err := http.NewRequest(post, u.String(), nil)
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

		if resp.StatusCode == http.StatusBadGateway {
			return fmt.Errorf("server: temporary error")
		} else if resp.StatusCode >= 300 {
			b, _ := ioutil.ReadAll(resp.Body)
			return backoff.Permanent(fmt.Errorf("server: %v", string(b)))
		}

		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxBackoffRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Image: error: %v", err)
			log.Printf("Image: retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Image: error: %v", err)
		errCh <- err
		return
	}
}
