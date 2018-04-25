package cmd

import (
	"bufio"
	"bytes"
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
	bodyBuf := &bytes.Buffer{}
	bodyWriter := multipart.NewWriter(bodyBuf)

	log.Printf("Packing request...\n")
	requirementsWriter, err := bodyWriter.CreateFormFile("requirements", "requirements.txt")
	if err != nil {
		log.Printf("Failed to create requirements.txt form file: %v\n", err)
		return nil, err
	}
	requirementsFile, err := os.Open(j.Requirements)
	if err != nil {
		log.Printf("Failed to open file %s: %v\n", j.Requirements, err)
		return nil, err
	}
	defer check.Err(requirementsFile.Close)
	_, err = io.Copy(requirementsWriter, requirementsFile)
	if err != nil {
		log.Printf("Failed to copy requirements file: %v\n", err)
		return nil, err
	}

	trainWriter, err := bodyWriter.CreateFormFile("train", "train.py")
	if err != nil {
		log.Printf("Failed to create train.py form file: %v\n", err)
		return nil, err
	}
	trainFile, err := os.Open(j.Train)
	if err != nil {
		log.Printf("Failed to open file %s: %v\n", j.Train, err)
		return nil, err
	}
	defer check.Err(trainFile.Close)
	_, err = io.Copy(trainWriter, trainFile)
	if err != nil {
		log.Printf("Failed to copy train file %s: %v\n", err)
		return nil, err
	}

	dataTarGzPath := j.Data + ".tar.gz"
	if err = archiver.TarGz.Make(dataTarGzPath, []string{j.Data}); err != nil {
		log.Printf("Failed to tar & gzip %s: %v\n", j.Data, err)
		return nil, err
	}
	defer check.Err(func() error { return os.Remove(dataTarGzPath) })
	dataWriter, err := bodyWriter.CreateFormFile("data", "data.tar.gz")
	if err != nil {
		log.Printf("Failed to create data.tar.gz file: %v\n", err)
		return nil, err
	}
	dataTarGzFile, err := os.Open(dataTarGzPath)
	if err != nil {
		log.Printf("Failed to open file %s: %v\n", dataTarGzPath, err)
		return nil, err
	}
	defer check.Err(dataTarGzFile.Close)
	_, err = io.Copy(dataWriter, dataTarGzFile)
	if err != nil {
		log.Printf("Failed to copy data.tar.gz: %v\n", err)
		return nil, err
	}

	bodyWriter.Close()
	uPath, _ := url.Parse("/job/upload")
	base := resolveBase()
	url := base.ResolveReference(uPath)
	log.Printf("Sending request...\n")
	req, err := http.NewRequest("POST", url.String(), bodyBuf)
	if err != nil {
		log.Printf("Failed to create new http request: %v\n", err)
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", j.Token))
	contentType := bodyWriter.FormDataContentType()
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Content-Encoding", "gzip")

	client := resolveClient()
	return client.Do(req)
}
