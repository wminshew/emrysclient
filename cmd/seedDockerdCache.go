package cmd

import (
	"context"
	"docker.io/go-docker"
	"docker.io/go-docker/api/types"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/jsonmessage"
	"log"
	"os"
	"time"
)

func seedDockerdCache(ctx context.Context, dClient *docker.Client, dockerAuthStr string) error {
	log.Printf("Pulling base image to seed dockerd cache...\n")

	registry := "registry.emrys.io"
	repo := "emrys"
	img := "base"
	tag := "1604-90"
	refStr := fmt.Sprintf("%s/%s/%s:%s", registry, repo, img, tag)
	operation := func() error {
		pullResp, err := dClient.ImagePull(ctx, refStr, types.ImagePullOptions{
			RegistryAuth: dockerAuthStr,
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
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 3), ctx),
		func(err error, t time.Duration) {
			log.Printf("Error pulling base image: %v", err)
			log.Printf("Retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		return fmt.Errorf("Error pulling base image: %v", err)
	}

	log.Printf("Base image pulled\n")
	return nil
}
