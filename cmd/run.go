package cmd

import (
	"bytes"
	"fmt"
	"github.com/mholt/archiver"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
			log.Printf("Error reading config file; using defaults")
		}

		job := &job{
			Token:        token,
			Requirements: viper.GetString("requirements"),
			Train:        viper.GetString("train"),
			Data:         viper.GetString("data"),
		}

		resp, err := postJob(job)
		if err != nil {
			log.Fatalf("Error posting your job: %v\n", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Fatalf("Request error: %v\n", resp.Status)
		}

		buf := new(bytes.Buffer)
		io.Copy(buf, resp.Body)
		fmt.Println(buf.String())
	},
}

func postJob(j *job) (*http.Response, error) {
	bodyBuf := &bytes.Buffer{}
	bodyWriter := multipart.NewWriter(bodyBuf)

	requirementsWriter, err := bodyWriter.CreateFormFile("requirements", "requirements.txt")
	if err != nil {
		log.Fatalf("Failed to create requirements.txt form file: %v\n", err)
	}
	requirementsFile, err := os.Open(j.Requirements)
	if err != nil {
		log.Fatalf("Failed to open file %s: %v\n", j.Requirements, err)
	}
	defer requirementsFile.Close()
	_, err = io.Copy(requirementsWriter, requirementsFile)
	if err != nil {
		log.Fatalf("Failed to copy requirements file: %v\n", err)
	}

	trainWriter, err := bodyWriter.CreateFormFile("train", "train.py")
	if err != nil {
		log.Fatalf("Failed to create train.py form file: %v\n", err)
	}
	trainFile, err := os.Open(j.Train)
	if err != nil {
		log.Fatalf("Failed to open file %s: %v\n", j.Train, err)
	}
	defer trainFile.Close()
	_, err = io.Copy(trainWriter, trainFile)
	if err != nil {
		log.Fatalf("Failed to copy train file %s: %v\n", err)
	}

	// add Data to PostForm, if appropriate
	// TODO: add DataURL to PostForm, if approriate; ideally you can just specify
	// --data [path|url] and the system takes care of the rest
	// then again, if its a url you should probably be downloading it and arranging
	// it specifically within train.py......
	dataTarGzPath := j.Data + ".tar.gz"
	if err = archiver.TarGz.Make(dataTarGzPath, []string{j.Data}); err != nil {
		log.Fatalf("Failed to tar & gzip %s: %v\n", j.Data, err)
	}
	// TODO: figure out why this isn't executing when the connection is refused
	defer os.Remove(dataTarGzPath)

	dataWriter, err := bodyWriter.CreateFormFile("data", "data.tar.gz")
	if err != nil {
		log.Fatalf("Failed to create data.tar.gz file: %v\n", err)
	}
	dataTarGzFile, err := os.Open(dataTarGzPath)
	if err != nil {
		log.Fatalf("Failed to open file %s: %v\n", dataTarGzPath, err)
	}
	defer dataTarGzFile.Close()
	_, err = io.Copy(dataWriter, dataTarGzFile)
	if err != nil {
		log.Fatalf("Failed to copy data.tar.gz: %v\n", err)
	}

	bodyWriter.Close()
	uPath, _ := url.Parse("/job/upload")
	base := resolveBase()
	url := base.ResolveReference(uPath)
	req, err := http.NewRequest("POST", url.String(), bodyBuf)
	if err != nil {
		log.Fatalf("Failed to create new http request: %v\n", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", j.Token))
	contentType := bodyWriter.FormDataContentType()
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Content-Encoding", "gzip")

	client := resolveClient()
	return client.Do(req)
}
