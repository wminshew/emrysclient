package cmd

import (
	"context"
	"docker.io/go-docker"
	"docker.io/go-docker/api/types"
	"encoding/base64"
	"encoding/json"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/jsonmessage"
	"log"
	"os"
	"sync"
)

func downloadImage(ctx context.Context, wg *sync.WaitGroup, errCh chan<- error, cli *docker.Client, refStr, authToken string) {
	defer wg.Done()
	log.Printf("Downloading image...\n")
	authConfig := types.AuthConfig{
		RegistryToken: authToken,
	}
	authJSON, err := json.Marshal(authConfig)
	if err != nil {
		log.Printf("Error json marshaling auth config: %v", err)
		errCh <- err
		return
	}
	authStr := base64.URLEncoding.EncodeToString(authJSON)
	pullResp, err := cli.ImagePull(ctx, refStr, types.ImagePullOptions{
		RegistryAuth: authStr,
	})
	if err != nil {
		log.Printf("Error downloading image: %v", err)
		errCh <- err
		return
	}
	defer check.Err(pullResp.Close)

	if err := jsonmessage.DisplayJSONMessagesStream(pullResp, os.Stdout, os.Stdout.Fd(), nil); err != nil {
		log.Printf("Error downloading image: %v", err)
		errCh <- err
		return
	}
}
