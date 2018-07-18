package cmd

import (
	"errors"
	"fmt"
	"github.com/dgrijalva/jwt-go"
	"github.com/mholt/archiver"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wminshew/emrys/pkg/check"
	"io"
	"io/ioutil"
	"mime/multipart"
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
			fmt.Printf("Error parsing authToken %v: %v\n", authToken, err)
			return
		}
		if err = claims.Valid(); err != nil {
			fmt.Printf("Error invalid authToken claims: %v\n", err)
			fmt.Printf("Please login again.\n")
			return
		}
		uID := claims.Subject
		exp := claims.ExpiresAt
		remaining := time.Until(time.Unix(exp, 0))
		if remaining <= 24*time.Hour {
			fmt.Printf("Warning: login token expires in apprx. ~%.f hours\n", remaining.Hours())
		}

		if err := checkVersion(); err != nil {
			fmt.Printf("Version error: %v\n", err)
			return
		}

		viper.SetConfigName(viper.GetString("config"))
		viper.AddConfigPath(".")
		err = viper.ReadInConfig()
		if err != nil {
			fmt.Printf("Error reading config file: %v\n", err)
			return
		}

		j := &jobReq{
			requirements: viper.GetString("requirements"),
			main:         viper.GetString("main"),
			data:         viper.GetString("data"),
			output:       viper.GetString("output"),
		}
		if err = checkJobReq(j); err != nil {
			fmt.Printf("Error with user-defined job requirements: %v\n", err)
			return
		}

		client := &http.Client{}
		s := "https"
		h := resolveHost()
		p := path.Join("user", uID, "job")
		u := url.URL{
			Scheme: s,
			Host:   h,
			Path:   p,
		}
		fmt.Printf("Sending job requirements...\n")
		req, err := createJobReq(u, authToken, j)
		if err != nil {
			fmt.Printf("Error creating request POST %v: %v\n", p, err)
			return
		}

		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("Error %v %v: %v\n", req.Method, p, err)
			return
		}

		if resp.StatusCode != http.StatusOK {
			fmt.Printf("Response error header: %v\n", resp.Status)
			b, _ := ioutil.ReadAll(resp.Body)
			fmt.Printf("Response error detail: %s\n", b)
			check.Err(resp.Body.Close)
			return
		}
		fmt.Printf("Job requirements sent!\n")

		jobToken := resp.Header.Get("Set-Job-Authorization")
		if jobToken == "" {
			fmt.Printf("Error: Received no job authorization token.\n")
			check.Err(resp.Body.Close)
			return
		}
		check.Err(resp.Body.Close)

		claims = &jwt.StandardClaims{}
		_, _, err = new(jwt.Parser).ParseUnverified(jobToken, claims)
		if err != nil {
			fmt.Printf("Error parsing jobToken %v: %v\n", jobToken, err)
			return
		}
		jID := claims.Subject
		jobPath := path.Join("user", uID, "job", jID)

		fmt.Printf("Building image...\n")
		p = path.Join(jobPath, "image")
		u.Path = p
		req, err = http.NewRequest("POST", u.String(), nil)
		if err != nil {
			fmt.Printf("Error creating request POST %v: %v\n", p, err)
			return
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
		req.Header.Set("Job-Authorization", jobToken)

		// fmt.Printf("Sending %v %v...\n", req.Method, p)
		resp, err = client.Do(req)
		if err != nil {
			fmt.Printf("Error %v %v: %v\n", req.Method, p, err)
			return
		}

		if resp.StatusCode != http.StatusOK {
			fmt.Printf("Response error header: %v\n", resp.Status)
			b, _ := ioutil.ReadAll(resp.Body)
			fmt.Printf("Response error detail: %s\n", b)
			check.Err(resp.Body.Close)
			return
		}
		fmt.Printf("Image built!\n")

		fmt.Printf("Running auction...\n")
		p = path.Join(jobPath, "auction")
		u.Path = p
		req, err = http.NewRequest("POST", u.String(), nil)
		if err != nil {
			fmt.Printf("Error creating request POST %v: %v\n", p, err)
			return
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
		req.Header.Set("Job-Authorization", jobToken)

		// fmt.Printf("Sending %v %v...\n", req.Method, p)
		resp, err = client.Do(req)
		if err != nil {
			fmt.Printf("Error %v %v: %v\n", req.Method, p, err)
			return
		}

		if resp.StatusCode != http.StatusOK {
			fmt.Printf("Response error header: %v\n", resp.Status)
			b, _ := ioutil.ReadAll(resp.Body)
			fmt.Printf("Response error detail: %s\n", b)
			check.Err(resp.Body.Close)
			return
		}
		fmt.Printf("Auction completed!\n")

		fmt.Printf("Streaming output log... (may take a few minutes to begin)\n")
		p = path.Join(jobPath, "output", "log")
		u.Path = p
		req, err = http.NewRequest("GET", u.String(), nil)
		if err != nil {
			fmt.Printf("Failed to create new http request: %v\n", err)
			return
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
		req.Header.Set("Job-Authorization", jobToken)

		// fmt.Printf("Sending %v %v...\n", req.Method, p)
		resp, err = client.Do(req)
		if err != nil {
			fmt.Printf("Error %v %v: %v\n", req.Method, p, err)
			return
		}

		if resp.StatusCode != http.StatusOK {
			fmt.Printf("Response error header: %v\n", resp.Status)
			b, _ := ioutil.ReadAll(resp.Body)
			fmt.Printf("Response error detail: %s\n", b)
			check.Err(resp.Body.Close)
			return
		}

		_, err = io.Copy(os.Stdout, resp.Body)
		if err != nil {
			fmt.Printf("Error copying response body: %v\n", err)
			check.Err(resp.Body.Close)
			return
		}
		check.Err(resp.Body.Close)

		fmt.Printf("Downloading output directory... (may take a few minutes to complete)\n")
		p = path.Join(jobPath, "output", "dir")
		u.Path = p
		req, err = http.NewRequest("GET", u.String(), nil)
		if err != nil {
			fmt.Printf("Failed to create new http request: %v\n", err)
			return
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
		req.Header.Set("Job-Authorization", jobToken)

		// fmt.Printf("Sending %v %v...\n", req.Method, p)
		resp, err = client.Do(req)
		if err != nil {
			fmt.Printf("Error %v %v: %v\n", req.Method, p, err)
			return
		}

		if resp.StatusCode != http.StatusOK {
			fmt.Printf("Response error header: %v\n", resp.Status)
			b, _ := ioutil.ReadAll(resp.Body)
			fmt.Printf("Response error detail: %s\n", b)
			check.Err(resp.Body.Close)
			return
		}

		outputDir := filepath.Join(j.output, jID)
		if err = os.MkdirAll(outputDir, 0755); err != nil {
			fmt.Printf("Error making output dir %v: %v\n", outputDir, err)
			check.Err(resp.Body.Close)
			return
		}
		if err = archiver.TarGz.Read(resp.Body, outputDir); err != nil {
			fmt.Printf("Error unpacking .tar.gz into output dir %v: %v\n", outputDir, err)
			check.Err(resp.Body.Close)
			return
		}
		check.Err(resp.Body.Close)
		fmt.Printf("Job complete!\n")
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

func createJobReq(u url.URL, authToken string, j *jobReq) (*http.Request, error) {
	r, w := io.Pipe()
	bodyW := multipart.NewWriter(w)

	fmt.Printf("Packing request...\n")
	go func() {
		err := addFormFile(bodyW, "main", j.main)
		if err != nil {
			fmt.Printf("Failed to add main to POST: %v\n", err)
			_ = w.CloseWithError(err)
			check.Err(bodyW.Close)
			return
		}

		err = addFormFile(bodyW, "requirements", j.requirements)
		if err != nil {
			fmt.Printf("Failed to add requirements to POST: %v\n", err)
			_ = w.CloseWithError(err)
			check.Err(bodyW.Close)
			return
		}

		if j.data == "" {
			j.data, err = ioutil.TempDir(".", ".emrys-temp-data")
			if err != nil {
				fmt.Printf("Error creating temporary, empty data directory: %v\n", err)
				_ = w.CloseWithError(err)
				check.Err(bodyW.Close)
				return
			}
			defer check.Err(func() error { return os.RemoveAll(j.data) })
		}
		tempW, err := bodyW.CreateFormFile("data", "data.tar.gz")
		if err != nil {
			fmt.Printf("Failed to add j.data %v form file to POST: %v\n", j.data, err)
			_ = w.CloseWithError(err)
			check.Err(bodyW.Close)
			return
		}

		if err = archiver.TarGz.Write(tempW, []string{j.data}); err != nil {
			fmt.Printf("Error packing j.data dir %v: %v\n", j.data, err)
			_ = w.CloseWithError(err)
			check.Err(bodyW.Close)
			return
		}

		err = bodyW.Close()
		if err != nil {
			fmt.Printf("Failed to close request bodyW: %v\n", err)
			_ = w.CloseWithError(err)
			return
		}
		check.Err(w.Close)
	}()

	req, err := http.NewRequest("POST", u.String(), r)
	if err != nil {
		fmt.Printf("Failed to create new http request: %v\n", err)
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
	req.Header.Set("Content-Type", bodyW.FormDataContentType())

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
		fmt.Printf("Warning! Main (%v) will still only be able to save locally to "+
			"./output when executing, even though output (%v) has been set to a different "+
			"directory. Local output to ./output will be saved to your output (%v) at the end "+
			"of execution. If this is your intended workflow, please ignore this warning.\n",
			j.main, j.output, j.output)
	}
	return nil
}
