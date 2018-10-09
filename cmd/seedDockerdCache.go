package cmd

import (
	"context"
	"docker.io/go-docker"
	"docker.io/go-docker/api/types"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/jsonmessage"
	"log"
	"os"
)

func seedDockerdCache(ctx context.Context, authToken string) error {
	log.Printf("Pulling base image to seed dockerd cache...\n")

	registry := "registry.emrys.io"
	repo := "emrys"
	img := "base"
	tag := "1604-90"
	refStr := fmt.Sprintf("%s/%s/%s:%s", registry, repo, img, tag)
	cli, err := docker.NewEnvClient()
	if err != nil {
		return fmt.Errorf("error creating docker client: %v", err)
	}
	defer check.Err(cli.Close)
	authConfig := types.AuthConfig{
		RegistryToken: authToken,
	}
	authJSON, err := json.Marshal(authConfig)
	if err != nil {
		return fmt.Errorf("error json marshaling auth config: %v", err)
	}
	authStr := base64.URLEncoding.EncodeToString(authJSON)
	pullResp, err := cli.ImagePull(ctx, refStr, types.ImagePullOptions{
		RegistryAuth: authStr,
	})
	if err != nil {
		return fmt.Errorf("error pulling base image: %v", err)
	}
	defer check.Err(pullResp.Close)

	if err := jsonmessage.DisplayJSONMessagesStream(pullResp, os.Stdout, os.Stdout.Fd(), nil); err != nil {
		return fmt.Errorf("error displaying pull response: %v", err)
	}
	log.Printf("Base image pulled\n")
	return nil
}
