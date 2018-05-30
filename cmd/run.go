package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/dgrijalva/jwt-go"
	"github.com/mholt/archiver"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wminshew/emrys/pkg/check"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
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
	Long: `Syncs the appropriate maining files & data
	with the central server, then locates the cheapest
	spare GPU cycles on the internet to execute your
	job`,
	Run: func(cmd *cobra.Command, args []string) {
		authToken := getToken()
		claims := &jwt.StandardClaims{}
		_, _, err := new(jwt.Parser).ParseUnverified(authToken, claims)
		if err != nil {
			log.Printf("Error parsing authToken %v: %v\n", authToken, err)
			return
		}
		if err = claims.Valid(); err != nil {
			log.Printf("Error invalid authToken claims: %v\n", err)
			log.Printf("Please login again.\n")
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

		client := resolveClient()

		p := path.Join("user", "job")
		req, err := postJobReq(p, authToken, j)
		if err != nil {
			log.Printf("Error creating request POST %v: %v\n", p, err)
			return
		}

		log.Printf("Sending %v %v...\n", req.Method, p)
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Error %v %v: %v\n", req.Method, p, err)
			return
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("Response header error: %v\n", resp.Status)
			check.Err(resp.Body.Close)
			return
		}

		jobToken := resp.Header.Get("Set-Job-Authorization")
		if jobToken == "" {
			log.Printf("Error: Received no job authorization token.\n")
			check.Err(resp.Body.Close)
			return
		}

		r := bufio.NewReader(resp.Body)
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				break
			}

			log.Print(string(line))
		}

		_, _ = io.Copy(ioutil.Discard, resp.Body)
		check.Err(resp.Body.Close)

		claims = &jwt.StandardClaims{}
		_, _, err = new(jwt.Parser).ParseUnverified(jobToken, claims)
		if err != nil {
			log.Printf("Error parsing jobToken %v: %v\n", jobToken, err)
			return
		}

		jID := claims.Subject
		p = path.Join("user", "job", jID, "output", "log")
		req, err = getJobOutput(p, authToken, jobToken)
		if err != nil {
			log.Printf("Error creating request GET %v: %v\n", p, err)
			return
		}

		log.Printf("Sending %v %v...\n", req.Method, p)
		resp, err = client.Do(req)
		if err != nil {
			log.Printf("Error %v %v: %v\n", req.Method, p, err)
			return
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("Response header error: %v\n", resp.Status)
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

		p = path.Join("user", "job", jID, "output", "dir")
		req, err = getJobOutput(p, authToken, jobToken)
		if err != nil {
			log.Printf("Error creating request GET %v: %v\n", p, err)
			return
		}

		log.Printf("Sending %v %v...\n", req.Method, p)
		resp, err = client.Do(req)
		if err != nil {
			log.Printf("Error %v %v: %v\n", req.Method, p, err)
			return
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("Response header error: %v\n", resp.Status)
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

func addFormFile(w *multipart.Writer, name, fpath string) error {
	tempW, err := w.CreateFormFile(name, filepath.Base(fpath))
	if err != nil {
		return err
	}

	file, err := os.Open(fpath)
	if err != nil {
		return err
	}
	defer check.Err(file.Close)

	_, err = io.Copy(tempW, file)
	if err != nil {
		return err
	}
	return nil
}

func postJobReq(p, authToken string, j *jobReq) (*http.Request, error) {
	r, w := io.Pipe()
	bodyW := multipart.NewWriter(w)

	log.Printf("Packing request...\n")
	go func() {
		err := addFormFile(bodyW, "main", j.main)
		if err != nil {
			log.Printf("Failed to add main to POST: %v\n", err)
			_ = w.CloseWithError(err)
			check.Err(bodyW.Close)
			return
		}

		err = addFormFile(bodyW, "requirements", j.requirements)
		if err != nil {
			log.Printf("Failed to add requirements to POST: %v\n", err)
			_ = w.CloseWithError(err)
			check.Err(bodyW.Close)
			return
		}

		if j.data == "" {
			j.data, err = ioutil.TempDir(".", ".emrys-temp-data")
			if err != nil {
				log.Printf("Error creating temporary, empty data directory: %v\n", err)
				_ = w.CloseWithError(err)
				check.Err(bodyW.Close)
				return
			}
			defer check.Err(func() error { return os.RemoveAll(j.data) })
		}
		tempW, err := bodyW.CreateFormFile("data", "data.tar.gz")
		if err != nil {
			log.Printf("Failed to add j.data %v form file to POST: %v\n", j.data, err)
			_ = w.CloseWithError(err)
			check.Err(bodyW.Close)
			return
		}

		if err = archiver.TarGz.Write(tempW, []string{j.data}); err != nil {
			log.Printf("Error packing j.data dir %v: %v\n", j.data, err)
			_ = w.CloseWithError(err)
			check.Err(bodyW.Close)
			return
		}

		err = bodyW.Close()
		if err != nil {
			log.Printf("Failed to close request bodyW: %v\n", err)
			_ = w.CloseWithError(err)
			return
		}
		check.Err(w.Close)
	}()

	h := resolveHost()
	u := url.URL{
		Scheme: "https",
		Host:   h,
		Path:   p,
	}
	req, err := http.NewRequest("POST", u.String(), r)
	if err != nil {
		log.Printf("Failed to create new http request: %v\n", err)
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
	req.Header.Set("Content-Type", bodyW.FormDataContentType())

	return req, nil
}

func getJobOutput(p, authToken, jobToken string) (*http.Request, error) {
	h := resolveHost()
	u := url.URL{
		Scheme: "https",
		Host:   h,
		Path:   p,
	}
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		log.Printf("Failed to create new http request: %v\n", err)
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
	req.Header.Set("Job-Authorization", jobToken)

	return req, nil
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
		log.Printf(`Warning! Main (%v) will still only be able to save locally to ./output when executing, 
		even though output (%v) has been set to a different directory. Local output to ./output will be saved
		to your output (%v) at the end of execution. If this is your intended workflow,
		please ignore this warning.`, j.main, j.output, j.output)
		log.Println()
	}
	return nil
}
