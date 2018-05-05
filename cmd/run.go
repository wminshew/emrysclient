package cmd

import (
	"bufio"
	"fmt"
	"github.com/mholt/archiver"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wminshew/check"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
)

type job struct {
	Token        string
	Requirements string
	Train        string
	Data         string
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Dispatch a deep learning job",
	Long: `Syncs the appropriate training files & data
	with the central server, then locates the cheapest
	spare GPU cycles on the internet to execute your
	job`,
	Run: func(cmd *cobra.Command, args []string) {
		// TODO: check token validity before sending files;
		// files could be realllly large...
		token := getToken()

		viper.SetConfigName(viper.GetString("config"))
		viper.AddConfigPath(".")
		err := viper.ReadInConfig()
		if err != nil {
			log.Printf("Error reading config file")
			return
		}

		job := &job{
			Token:        token,
			Requirements: viper.GetString("requirements"),
			Train:        viper.GetString("train"),
			Data:         viper.GetString("data"),
		}

		resp, err := postJob(job)
		if err != nil {
			log.Printf("Error posting your job: %v\n", err)
			return
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode != http.StatusOK {
			log.Printf("Request error: %v\n", resp.Status)
			return
		}

		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				break
			}

			log.Print(string(line))
		}
	},
}

func postJob(j *job) (*http.Response, error) {
	r, w := io.Pipe()
	bodyW := multipart.NewWriter(w)

	log.Printf("Packing request...\n")
	go func() {
		defer check.Err(w.Close)

		err := addFormFile(bodyW, "requirements", "requirements.txt", j.Requirements)
		if err != nil {
			log.Printf("Failed to add requirements to POST: %v\n", err)
			_ = w.CloseWithError(err)
			return
		}

		err = addFormFile(bodyW, "train", "train.py", j.Train)
		if err != nil {
			log.Printf("Failed to add train to POST: %v\n", err)
			_ = w.CloseWithError(err)
			return
		}

		dataTarGzPath := j.Data + ".tar.gz"
		if err = archiver.TarGz.Make(dataTarGzPath, []string{j.Data}); err != nil {
			log.Printf("Failed to tar & gzip %s: %v\n", j.Data, err)
			_ = w.CloseWithError(err)
			return
		}
		defer check.Err(func() error { return os.Remove(dataTarGzPath) })
		err = addFormFile(bodyW, "data", "data.tar.gz", dataTarGzPath)
		if err != nil {
			log.Printf("Failed to add data to POST: %v\n", err)
			_ = w.CloseWithError(err)
			return
		}

		err = bodyW.Close()
		if err != nil {
			log.Printf("Failed to close request bodyW: %v\n", err)
			_ = w.CloseWithError(err)
			return
		}
	}()

	h := resolveHost()
	u := url.URL{
		Scheme: "https",
		Host:   h,
		Path:   "/user/job/new",
	}
	req, err := http.NewRequest("POST", u.String(), r)
	if err != nil {
		log.Printf("Failed to create new http request: %v\n", err)
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", j.Token))
	req.Header.Set("Content-Type", bodyW.FormDataContentType())

	client := resolveClient()
	log.Printf("Sending request...\n")
	return client.Do(req)
}

func addFormFile(w *multipart.Writer, name, filename, filepath string) error {
	tempW, err := w.CreateFormFile(name, filename)
	if err != nil {
		return err
	}

	file, err := os.Open(filepath)
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
