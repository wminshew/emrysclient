package cmd

import (
	"context"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/dgrijalva/jwt-go"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wminshew/emrys/pkg/check"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
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
		authToken, err := getToken()
		if err != nil {
			return
		}
		claims := &jwt.StandardClaims{}
		_, _, err = new(jwt.Parser).ParseUnverified(authToken, claims)
		if err != nil {
			log.Printf("Error parsing authToken %v: %v\n", authToken, err)
			return
		}
		if err = claims.Valid(); err != nil {
			log.Printf("Error invalid authToken claims: %v\n", err)
			log.Printf("Please login again.\n")
			return
		}
		uID := claims.Subject
		exp := claims.ExpiresAt
		remaining := time.Until(time.Unix(exp, 0))
		if remaining <= 24*time.Hour {
			log.Printf("Warning: login token expires in apprx. ~%.f hours\n", remaining.Hours())
		}

		client := &http.Client{}
		ctx := context.Background()
		s := "https"
		h := resolveHost()
		u := url.URL{
			Scheme: s,
			Host:   h,
		}
		if err := checkVersion(client, u); err != nil {
			log.Printf("Version error: %v\n", err)
			return
		}

		viper.SetConfigName(viper.GetString("config"))
		viper.AddConfigPath(".")
		if err := viper.ReadInConfig(); err != nil {
			log.Printf("Error reading config file: %v\n", err)
			return
		}

		j := &jobReq{
			project:      viper.GetString("project"),
			requirements: viper.GetString("requirements"),
			main:         viper.GetString("main"),
			data:         viper.GetString("data"),
			output:       viper.GetString("output"),
		}
		if err = j.validate(); err != nil {
			log.Printf("Error with user-defined job requirements: %v\n", err)
			return
		}

		m := "POST"
		p := path.Join("user", uID, "project", j.project, "job")
		u.Path = p
		log.Printf("Sending job requirements...\n")

		req, err := http.NewRequest(m, u.String(), nil)
		if err != nil {
			log.Printf("error creating request %v %v: %v\n", m, p, err)
			return
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

		var jID string
		operation := func() error {
			resp, err := client.Do(req)
			if err != nil {

			}
			defer check.Err(resp.Body.Close)

			if resp.StatusCode != http.StatusOK {
				b, _ := ioutil.ReadAll(resp.Body)
				return fmt.Errorf("server response: %v", b)
			}
			jID = resp.Header.Get("X-Job-ID")

			return nil
		}
		if err := backoff.RetryNotify(operation,
			backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5), ctx),
			func(err error, t time.Duration) {
				log.Printf("Error sending job requirements: %v", err)
				log.Printf("Trying again in %s seconds\n", t.Round(time.Second).String())
			}); err != nil {
			log.Printf("Error sending job requirements: %v", err)
			return
		}

		log.Printf("Job requirements sent!\n")

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
		case <-done:
		case <-errCh:
			return
		}

		success, err := runAuction(ctx, client, u, jID, authToken)
		if err != nil {
			log.Printf("Auction error: %v\n", err)
			return
		} else if !success {
			return
		}

		outputDir := filepath.Join(j.output, jID)
		if err = os.MkdirAll(outputDir, 0755); err != nil {
			log.Printf("Output data: error making output dir %v: %v\n", outputDir, err)
			return
		}

		log.Printf("Executing job %s\n", jID)
		if err := streamOutputLog(ctx, client, u, jID, authToken, j.output); err != nil {
			log.Printf("Output log: error: %v\n", err)
		}
		buffer := 1 * time.Second
		time.Sleep(buffer)
		if err := downloadOutputData(ctx, client, u, jID, authToken, j.output); err != nil {
			log.Printf("Output data: error: %v\n", err)
		}

		log.Printf("Job complete!\n")
	},
}
