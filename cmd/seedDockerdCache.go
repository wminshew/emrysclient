package cmd

import (
	"context"
	"docker.io/go-docker"
	"docker.io/go-docker/api/types"
	"fmt"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/jsonmessage"
	"log"
	"os"
)

func seedDockerdCache(ctx context.Context) error {
	log.Printf("Pulling base image to seed dockerd cache...\n")

	registry := "registry.emrys.io"
	repo := "emrys"
	img := "base"
	tag := "1604-90"
	refStr := fmt.Sprintf("%s/%s/%s:%s", registry, repo, img, tag)
	cli, err := docker.NewEnvClient()
	if err != nil {
		log.Printf("Error creating docker client: %v", err)
		return err
	}
	defer check.Err(cli.Close)
	pullResp, err := cli.ImagePull(ctx, refStr, types.ImagePullOptions{
		RegistryAuth: "none",
	})
	if err != nil {
		log.Printf("Error pulling base image: %v", err)
		return err
	}
	defer check.Err(pullResp.Close)

	if err := jsonmessage.DisplayJSONMessagesStream(pullResp, os.Stdout, os.Stdout.Fd(), nil); err != nil {
		log.Printf("Error displaying pull response: %v", err)
		return err
	}
	log.Printf("Base image pulled\n")
	return nil
}
