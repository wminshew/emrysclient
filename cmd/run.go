package cmd

import (
	"context"
	"errors"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/dgrijalva/jwt-go"
	"github.com/mholt/archiver"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wminshew/emrys/pkg/check"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"time"
)

type jobReq struct {
	requirements string
	main         string
	data         string
	output       string
}

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
		operation := func() error {
			return checkVersion(client, u)
		}
		if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
			log.Printf("Version error: %v\n", err)
			return
		}

		viper.SetConfigName(viper.GetString("config"))
		viper.AddConfigPath(".")
		err = viper.ReadInConfig()
		if err != nil {
			log.Printf("Error reading config file: %v\n", err)
			return
		}

		j := &jobReq{
			requirements: viper.GetString("requirements"),
			main:         viper.GetString("main"),
			data:         viper.GetString("data"),
			output:       viper.GetString("output"),
		}
		if err = checkJobReq(j); err != nil {
			log.Printf("Error with user-defined job requirements: %v\n", err)
			return
		}

		m := "POST"
		p := path.Join("user", uID, "job")
		u.Path = p
		log.Printf("Sending job requirements...\n")

		req, err := http.NewRequest(m, u.String(), nil)
		if err != nil {
			log.Printf("error creating request %v %v: %v\n", m, p, err)
			return
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

		var resp *http.Response
		operation = func() error {
			var err error
			resp, err = client.Do(req)
			return err
		}
		if err := backoff.Retry(operation, backoff.NewExponentialBackOff()); err != nil {
			log.Printf("Error %v %v: %v\n", req.Method, p, err)
			return
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("Response error header: %v\n", resp.Status)
			b, _ := ioutil.ReadAll(resp.Body)
			log.Printf("Response error detail: %s\n", b)
			check.Err(resp.Body.Close)
			return
		}
		log.Printf("Job requirements sent!\n")

		jID := resp.Header.Get("X-Job-ID")
		check.Err(resp.Body.Close)

		// go runAuction(ctx, client, u, jID, authToken)
		// runAuction(ctx, client, u, jID, authToken)
		// go buildImage(ctx, client, u, jID, authToken, j.main, j.requirements)
		buildImage(ctx, client, u, jID, authToken, j.main, j.requirements)
		// go syncData(ctx, client, u, jID, authToken, []string{j.data})
		// go syncData(ctx, client, u, jID, authToken, []string{j.data})

		time.Sleep(60 * time.Second)

		log.Printf("Streaming output log... (may take a few minutes to begin)\n")
		m = "GET"
		p = path.Join("job", jID, "output", "log")
		u.Path = p
		req, err = http.NewRequest(m, u.String(), nil)
		if err != nil {
			log.Printf("error creating request %v %v: %v\n", m, p, err)
			return
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

		resp, err = client.Do(req)
		if err != nil {
			log.Printf("Error %v %v: %v\n", req.Method, p, err)
			return
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("Response error header: %v\n", resp.Status)
			b, _ := ioutil.ReadAll(resp.Body)
			log.Printf("Response error detail: %s\n", b)
			check.Err(resp.Body.Close)
			return
		}

		_, err = io.Copy(os.Stdout, resp.Body)
		if err != nil {
			log.Printf("Error copying response body: %v\n", err)
			check.Err(resp.Body.Close)
			return
		}
		check.Err(resp.Body.Close)

		log.Printf("Downloading output directory... (may take a few minutes to complete)\n")
		m = "GET"
		p = path.Join("job", jID, "output", "dir")
		u.Path = p
		req, err = http.NewRequest(m, u.String(), nil)
		if err != nil {
			log.Printf("error creating request %v %v: %v\n", m, p, err)
			return
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))

		resp, err = client.Do(req)
		if err != nil {
			log.Printf("Error %v %v: %v\n", req.Method, p, err)
			return
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("Response error header: %v\n", resp.Status)
			b, _ := ioutil.ReadAll(resp.Body)
			log.Printf("Response error detail: %s\n", b)
			check.Err(resp.Body.Close)
			return
		}

		outputDir := filepath.Join(j.output, jID)
		if err = os.MkdirAll(outputDir, 0755); err != nil {
			log.Printf("Error making output dir %v: %v\n", outputDir, err)
			check.Err(resp.Body.Close)
			return
		}
		if err = archiver.TarGz.Read(resp.Body, outputDir); err != nil {
			log.Printf("Error unpacking .tar.gz into output dir %v: %v\n", outputDir, err)
			check.Err(resp.Body.Close)
			return
		}
		check.Err(resp.Body.Close)
		log.Printf("Job complete!\n")
	},
}

func checkJobReq(j *jobReq) error {
	if j.main == "" {
		return errors.New("must specify a main execution file in config or with flag")
	}
	if j.requirements == "" {
		return errors.New("must specify a requirements file in config or with flag")
	}
	if j.output == "" {
		return errors.New("must specify an output directory in config or with flag")
	}
	if j.data == j.output {
		return errors.New("can't use same directory for data and output")
	}
	if filepath.Base(j.data) == "output" {
		return errors.New("can't name data directory \"output\"")
	}
	if j.data != "" {
		if filepath.Dir(j.main) != filepath.Dir(j.data) {
			return fmt.Errorf("main (%v) and data (%v) must be in the same directory", j.main, j.data)
		}
	}
	if filepath.Dir(j.main) != filepath.Dir(j.output) {
		log.Printf("Warning! Main (%v) will still only be able to save locally to "+
			"./output when executing, even though output (%v) has been set to a different "+
			"directory. Local output to ./output will be saved to your output (%v) at the end "+
			"of execution. If this is your intended workflow, please ignore this warning.\n",
			j.main, j.output, j.output)
	}
	return nil
}
