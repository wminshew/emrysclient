package cmd

import (
	"context"
	"github.com/dgrijalva/jwt-go"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Dispatch a deep learning job",
	Long: "Syncs the appropriate maining files & data " +
		"with the central server, then locates the cheapest " +
		"spare GPU cycles on the internet to execute your job",
	Run: func(cmd *cobra.Command, args []string) {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-stop
			log.Printf("Cancellation request received: please wait for job to successfully cancel\n")
			log.Printf("Warning: failure to successfully cancel job may result in undesirable charges\n")
			cancel()
		}()

		authToken, err := getToken()
		if err != nil {
			log.Printf("Error: retrieving authToken: %v", err)
			return
		}
		claims := &jwt.StandardClaims{}
		if _, _, err := new(jwt.Parser).ParseUnverified(authToken, claims); err != nil {
			log.Printf("Error: parsing authToken: %v", err)
			return
		}
		if err := claims.Valid(); err != nil {
			log.Printf("Error: invalid authToken: %v", err)
			log.Printf("Please login again.\n")
			return
		}
		uID := claims.Subject
		exp := claims.ExpiresAt
		remaining := time.Until(time.Unix(exp, 0))
		if remaining <= 24*time.Hour {
			log.Printf("Warning: token expires in apprx. ~%.f hours\n", remaining.Hours())
		}

		client := &http.Client{}
		s := "https"
		h := "api.emrys.io"
		u := url.URL{
			Scheme: s,
			Host:   h,
		}
		if err := checkVersion(ctx, client, u); err != nil {
			log.Printf("Version: error: %v", err)
			return
		}

		viper.SetConfigName(viper.GetString("config"))
		viper.AddConfigPath(".")
		if err := viper.ReadInConfig(); err != nil {
			log.Printf("Error: reading config file: %v", err)
			return
		}

		j := &jobReq{
			project:      viper.GetString("project"),
			requirements: viper.GetString("requirements"),
			main:         viper.GetString("main"),
			data:         viper.GetString("data"),
			output:       viper.GetString("output"),
		}
		if err := j.validate(); err != nil {
			log.Printf("Error: invalid job requirements: %v", err)
			return
		}
		var jID string
		if jID, err = j.send(ctx, client, u, uID, authToken); err != nil {
			log.Printf("Error: sending job requirements: %v", err)
			return
		}
		completed := false
		defer func() {
			if !completed {
				if err := j.cancel(client, u, uID, jID, authToken); err != nil {
					log.Printf("Error: canceling job: %v", err)
					return
				}
			}
		}()

		if err := checkContextCanceled(ctx); err != nil {
			return
		}
		errCh := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go buildImage(ctx, &wg, errCh, client, u, uID, j.project, jID, authToken, j.main, j.requirements)
		go syncData(ctx, &wg, errCh, client, u, uID, j.project, jID, authToken, j.data)
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-ctx.Done():
			return
		case <-done:
		case <-errCh:
			return
		}

		if err := runAuction(ctx, client, u, jID, authToken); err != nil {
			log.Printf("Auction: error: %v", err)
			return
		}

		if err := checkContextCanceled(ctx); err != nil {
			return
		}
		outputDir := filepath.Join(j.output, jID)
		if err = os.MkdirAll(outputDir, 0755); err != nil {
			log.Printf("Output data: error making output dir %v: %v", outputDir, err)
			return
		}

		log.Printf("Executing job %s\n", jID)
		if err := streamOutputLog(ctx, client, u, jID, authToken, j.output); err != nil {
			log.Printf("Output log: error: %v", err)
			return
		}
		buffer := 1 * time.Second
		time.Sleep(buffer)
		if err := downloadOutputData(ctx, client, u, jID, authToken, j.output); err != nil {
			log.Printf("Output data: error: %v", err)
			return
		}

		completed = true
		log.Printf("Complete!\n")
	},
}

func checkContextCanceled(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
