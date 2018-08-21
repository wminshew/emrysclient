package cmd

import (
	"context"
	"docker.io/go-docker"
	"docker.io/go-docker/api/types"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/jsonmessage"
	"log"
	"os"
	"sync"
)

func downloadImage(ctx context.Context, wg *sync.WaitGroup, errCh chan<- error, cli *docker.Client, refStr string) {
	defer wg.Done()
	log.Printf("Downloading image...\n")
	pullResp, err := cli.ImagePull(ctx, refStr, types.ImagePullOptions{
		RegistryAuth: "none",
	})
	if err != nil {
		log.Printf("Error downloading image: %v\n", err)
		errCh <- err
		return
	}
	defer check.Err(pullResp.Close)

	if err := jsonmessage.DisplayJSONMessagesStream(pullResp, os.Stdout, os.Stdout.Fd(), nil); err != nil {
		log.Printf("Error downloading image: %v\n", err)
		errCh <- err
		return
	}
}
