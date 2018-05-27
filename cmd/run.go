package cmd

import (
	"bufio"
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
			log.Printf("Error reading config file")
			return
		}

		j := &jobReq{
			requirements: viper.GetString("requirements"),
			main:         viper.GetString("main"),
			data:         viper.GetString("data"),
			output:       viper.GetString("output"),
		}

		client := resolveClient()

		p := path.Join("user", "job")
		req, err := postJobReq(p, authToken, j)
		if err != nil {
			log.Printf("Error creating request POST %v: %v\n", p, err)
			return
		}

		log.Printf("Sending POST %v...\n", p)
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Error POST %v: %v\n", p, err)
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

		// wd, err := os.Getwd()
		// if err != nil {
		// 	log.Printf("Error retrieving working directory to save down output directory: %v\n", err)
		// 	check.Err(resp.Body.Close)
		// 	return
		// }
		// outputDir := path.Join(wd, "output")
		// if err = archiver.TarGz.Read(resp.Body, outputDir); err != nil {
		if err = archiver.TarGz.Read(resp.Body, j.output); err != nil {
			log.Printf("Error unpacking .tar.gz into output dir %v: %v\n", j.output, err)
			check.Err(resp.Body.Close)
			return
		}
		check.Err(resp.Body.Close)
	},
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

func postJobReq(p, authToken string, j *jobReq) (*http.Request, error) {
	r, w := io.Pipe()
	bodyW := multipart.NewWriter(w)

	log.Printf("Packing request...\n")
	go func() {
		defer check.Err(w.Close)

		err := addFormFile(bodyW, "requirements", "requirements.txt", j.requirements)
		if err != nil {
			log.Printf("Failed to add requirements to POST: %v\n", err)
			_ = w.CloseWithError(err)
			return
		}

		err = addFormFile(bodyW, "main", "main.py", j.main)
		if err != nil {
			log.Printf("Failed to add main to POST: %v\n", err)
			_ = w.CloseWithError(err)
			return
		}

		// defer check.Err(pw.Close)
		// files, err := ioutil.ReadDir(hostOutputDir)
		// outputFiles := make([]string, len(files))
		// if err != nil {
		// 	log.Printf("Error reading files in hostOutputDir %v: %v\n", hostOutputDir, err)
		// 	return
		// }
		// for _, file := range files {
		// 	outputFiles = append(outputFiles, hostOutputDir+file.Name())
		// }
		// if err = archiver.TarGz.Write(pw, outputFiles); err != nil {
		// 	log.Printf("Error packing output dir %v: %v\n", hostOutputDir, err)
		// 	return
		// }
		dataTarGzPath := j.data + ".tar.gz"
		if err = archiver.TarGz.Make(dataTarGzPath, []string{j.data}); err != nil {
			log.Printf("Failed to tar & gzip %s: %v\n", j.data, err)
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
