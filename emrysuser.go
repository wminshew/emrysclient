package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/mholt/archiver"
	"gopkg.in/mattes/go-expand-tilde.v1"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httputil"
	"os"
	// "context"
)

type config struct {
	Username     string `json:"Username,omitempty"`
	Password     string `json:"Password,omitempty"`
	Env          string `json:"Env,omitempty"`
	Requirements string `json:"Requirements,omitempty"`
	Train        string `json:"Train,omitempty"`
	DataDir      string `json:"DataDir,omitempty"`
	Price        string `json:"Price,omitempty"`
}

func main() {
	// return useful info for help flags
	flag.Usage = func() {
		fmt.Printf("Usage of %s:\n", os.Args[0])
		fmt.Printf("  config or run subcommand is required\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	// establish config struct
	cfg := config{}

	// subcommands
	cfgCmd := flag.NewFlagSet("config", flag.ExitOnError)
	runCmd := flag.NewFlagSet("run", flag.ExitOnError)

	// global flags shared by subcommands
	var usernamePtr string
	var passwordPtr string
	var envPtr string
	var reqPtr string
	var trainPtr string
	var dataDirPtr string
	// TODO: create own struct implementing Value interface for price
	var pricePtr string
	var localCfg bool
	var globalCfg bool

	// cfg subcommand flag pointers
	cfgCmd.StringVar(&usernamePtr, "username", "", "Set the local or global username.")
	cfgCmd.StringVar(&passwordPtr, "password", "", "Set the local or global password.")
	cfgCmd.StringVar(&envPtr, "env", "", "Set the local or global environment.")
	cfgCmd.StringVar(&reqPtr, "requirements", "", "Set the local or global default requirements.txt for additonal libraries outside Env.")
	cfgCmd.StringVar(&trainPtr, "train", "", "Set the local or global default train path.")
	cfgCmd.StringVar(&dataDirPtr, "data-dir", "", "Set the local or global default data-dir path.")
	// TODO: create own struct implementing Value interface for price
	cfgCmd.StringVar(&pricePtr, "price", "", "Set the local or global price per calc.")
	cfgCmd.BoolVar(&localCfg, "local", false, "Save config locally (saved in this directory).")
	cfgCmd.BoolVar(&globalCfg, "global", false, "Save config globally (saved in home directory).")

	// run subcommand flag pointers
	runCmd.StringVar(&usernamePtr, "username", "", "Username flag overrides local and global config settings. (required if not set in config)")
	runCmd.StringVar(&passwordPtr, "password", "", "Password flag overrides local and global config settings. (required if not set in config)")
	runCmd.StringVar(&envPtr, "env", "", "Environment to execute within {tensorflow:latest, pytorch:latest}.")
	runCmd.StringVar(&reqPtr, "requirements", "", "Packages to load in venv before executing. (optional)")
	runCmd.StringVar(&trainPtr, "train", "", "Code to execute. (required if not set in config)")
	runCmd.StringVar(&dataDirPtr, "data-dir", "", "Data to train & validate model with.")
	// TODO: create own struct implementing Value interface for price
	runCmd.StringVar(&pricePtr, "price", "", "Maximum acceptable price per calc. (required if not set in config)")

	// verify that a subcommand has been passed
	// os.Arg[0]	main command
	// os.Arg[1]	subcommand
	if len(os.Args) < 2 {
		fmt.Println("config or run subcommand is required")
		os.Exit(1)
	}

	// switch on the subcommand
	switch os.Args[1] {
	case "config":
		cfgCmd.Parse(os.Args[2:])
	case "run":
		runCmd.Parse(os.Args[2:])
	default:
		flag.PrintDefaults()
		os.Exit(1)
	}

	// executed parsed subcommand
	if cfgCmd.Parsed() {
		// config subcommand flag handling
		if usernamePtr != "" {
			cfg.Username = usernamePtr
		}
		if passwordPtr != "" {
			cfg.Password = passwordPtr
		}
		if envPtr != "" {
			cfg.Env = envPtr
		}
		if reqPtr != "" {
			cfg.Requirements = reqPtr
		}
		if trainPtr != "" {
			cfg.Train = trainPtr
		}
		if dataDirPtr != "" {
			cfg.DataDir = dataDirPtr
		}
		if pricePtr != "" {
			cfg.Price = pricePtr
		}
		fmt.Printf("Config: %v\n", cfg)

		if !localCfg && !globalCfg {
			fmt.Printf("Use --local or --global to specify which config to set.\n")
		}
		if localCfg {
			var localCfg config
			pwd, _ := os.Getwd()
			path := pwd + "/.emrysconfig"

			// read the existing local config file
			if err := readConfig(path, &localCfg); err != nil {
				log.Fatalf("Error reading local config file: %v\n", err)
			}
			fmt.Printf("Previous local config: %v\n", localCfg)

			// updating local config based on current flags
			fmt.Printf("Updating local config...\n")
			if err := localCfg.updateConfig(cfg); err != nil {
				log.Fatalf("Error updating local config: %v\n", err)
			}
			fmt.Printf("New local config: %v\n", localCfg)

			// write the new local config file
			fmt.Printf("Writing to current working directory...\n")
			if err := WriteConfig(path, &localCfg); err != nil {
				log.Printf("Failed to save local config file: %v\n", err)
			} else {
				fmt.Printf("Success! Wrote to %s\n", path)
			}
		}

		if globalCfg {
			var globalCfg Config
			path, err := tilde.Expand("~/.emrysconfig")
			if err != nil {
				log.Fatalf("Couldn't expand tilde: %v\n", err)
			}

			// read the existing global config file
			if err := readConfig(path, &globalCfg); err != nil {
				log.Fatalf("Error reading global config file: %v\n", err)
			}
			fmt.Printf("Previous global config: %v\n", globalCfg)

			// updating global config based on current flags
			fmt.Printf("Updating global config...\n")
			if err := globalCfg.updateConfig(cfg); err != nil {
				log.Fatalf("Error updating global config: %v\n", err)
			}
			fmt.Printf("New global config: %v\n", globalCfg)

			// write the new global config file
			fmt.Printf("Writing to home directory...\n")
			if err := WriteConfig(path, &globalCfg); err != nil {
				log.Printf("Failed to save global config file: %v\n", err)
			} else {
				fmt.Printf("Success! Wrote to %s\n", path)
			}
		}
	}

	if runCmd.Parsed() {
		// run subcommand flag handling
		var globalCfg Config
		path, err := tilde.Expand("~/.emrysconfig")
		if err != nil {
			log.Fatalf("Couldn't expand tilde: %v\n", err)
		}

		// read the existing global config file into config for dispatch
		if err := readConfig(path, &globalCfg); err != nil {
			log.Printf("Error reading global config file: %v\n", err)
		}
		cfg.updateConfig(globalCfg)

		var localCfg Config
		pwd, _ := os.Getwd()
		path = pwd + "/.emrysconfig"

		// read the existing local config file and override any global settings
		if err := readConfig(path, &localCfg); err != nil {
			log.Printf("Error reading local config file: %v\n", err)
		}
		cfg.updateConfig(localCfg)

		// override any global or local config settings with flags
		if usernamePtr != "" {
			cfg.Username = usernamePtr
		}
		if passwordPtr != "" {
			cfg.Password = passwordPtr
		}
		if envPtr != "" {
			cfg.Env = envPtr
		}
		if reqPtr != "" {
			cfg.Requirements = reqPtr
		}
		if trainPtr != "" {
			cfg.Train = trainPtr
		}
		if dataDirPtr != "" {
			cfg.DataDir = dataDirPtr
		}
		if pricePtr != "" {
			cfg.Price = pricePtr
		}
		fmt.Printf("Config: %v\n", cfg)

		// required flags
		// TODO: replace with method ValidForRun() call on config or something..
		if cfg.Username == "" || cfg.Password == "" || cfg.Train == "" || cfg.DataDir == "" || cfg.Price == "" {
			runCmd.PrintDefaults()
			os.Exit(1)
		}

		// choice flags
		envChoices := map[string]bool{"tensorflow:latest": true, "pytorch:latest": true}
		if _, validChoices := envChoices[cfg.Env]; !validChoices {
			runCmd.PrintDefaults()
			log.Fatalf("Please choose a correct environment.")
			os.Exit(1)
		}

		// GET test
		// baseURL := "http://127.0.0.1:8080"
		// client := &http.Client{}
		// req, err := http.NewRequest("GET", baseURL, nil)
		// if err != nil {
		// 	log.Fatalln(err)
		// }
		// req.SetBasicAuth(cfg.Username, cfg.Password)
		// resp, err := client.Do(req)
		// if err != nil {
		// 	log.Fatalln(err)
		// }
		// defer resp.Body.Close()
		// body, err := ioutil.ReadAll(resp.Body)
		// fmt.Printf("%s\n", body)

		// POST file test
		// baseURL := "http://127.0.0.1:8080"
		// TODO: might have to get new certificate for server for this URL
		// and update cURLs
		baseURL := "https://wmdlserver.ddns.net:8080"
		client := &http.Client{}
		bodyBuf := &bytes.Buffer{}
		bodyWriter := multipart.NewWriter(bodyBuf)

		// add cfg params to PostForm
		if err := bodyWriter.WriteField("Env", cfg.Env); err != nil {
			log.Fatalf("Failed to write Env to PostForm: %v\n", err)
		}
		if err = bodyWriter.WriteField("Price", cfg.Price); err != nil {
			log.Fatalf("Failed to write Price to PostForm: %v\n", err)
		}

		// add Requirements file to PostForm [optional]
		requirementsWriter, err := bodyWriter.CreateFormFile("Requirements", cfg.Requirements)
		if err != nil {
			log.Fatalf("Failed to create form file %s: %v\n", cfg.Requirements, err)
		}
		requirementsFile, err := os.Open(cfg.Requirements)
		if err != nil {
			log.Fatalf("Failed to open file %s: %v\n", cfg.Requirements, err)
		}
		defer requirementsFile.Close()
		_, err = io.Copy(requirementsWriter, requirementsFile)
		if err != nil {
			log.Fatalf("Failed to copy file %s: %v\n", requirementsFile.Name(), err)
		}

		// add Train file to PostForm
		trainWriter, err := bodyWriter.CreateFormFile("Train", cfg.Train)
		if err != nil {
			log.Fatalf("Failed to create form file %s: %v\n", cfg.Train, err)
		}
		trainFile, err := os.Open(cfg.Train)
		if err != nil {
			log.Fatalf("Failed to open file %s: %v\n", cfg.Train, err)
		}
		defer trainFile.Close()
		_, err = io.Copy(trainWriter, trainFile)
		if err != nil {
			log.Fatalf("Failed to copy file %s: %v\n", trainFile.Name(), err)
		}

		// add DataDir to PostForm, if appropriate
		// archive & gzip data directory
		dataDirTarGzPath := cfg.DataDir + ".tar.gz"
		if err = archiver.TarGz.Make(dataDirTarGzPath, []string{cfg.DataDir}); err != nil {
			log.Fatalf("Failed to tar & gzip data dir %s: %v\n", dataDirTarGzPath, err)
		}
		// remove .tar.gz after POST
		// TODO: figure out why this isn't executing when the connection is refused
		defer os.Remove(dataDirTarGzPath)

		// write gzip'd data archive to PostForm
		dataDirWriter, err := bodyWriter.CreateFormFile("DataDir", dataDirTarGzPath)
		if err != nil {
			log.Fatalf("Failed to create form file %s: %v\n", dataDirTarGzPath, err)
		}
		dataDirTarGzFile, err := os.Open(dataDirTarGzPath)
		if err != nil {
			log.Fatalf("Failed to open file %s: %v\n", dataDirTarGzPath, err)
		}
		defer dataDirTarGzFile.Close()
		_, err = io.Copy(dataDirWriter, dataDirTarGzFile)
		if err != nil {
			log.Fatalf("Failed to copy file %s: %v\n", dataDirTarGzFile.Name(), err)
		}

		// TODO: add DataURL to PostForm, if approriate

		// add Form contentType & close Writer
		contentType := bodyWriter.FormDataContentType()
		bodyWriter.Close()

		// create request
		postTrainPy := "/job/upload"
		req, err := http.NewRequest("POST", baseURL+postTrainPy, bodyBuf)
		if err != nil {
			log.Fatalf("Failed to create new http request: %v\n", err)
		}
		req.SetBasicAuth(cfg.Username, cfg.Password)
		// req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Content-Type", contentType)
		// req.Header.Set("Content-Encoding", "gzip")

		// print request for debugging
		requestDump, err := httputil.DumpRequestOut(req, true)
		if err != nil {
			log.Println(err)
		}
		log.Println(string(requestDump))

		// send request
		resp, err := client.Do(req)
		if err != nil {
			log.Fatalln(err)
		}
		defer resp.Body.Close()

		// print response for debugging
		respDump, err := httputil.DumpResponse(resp, true)
		if err != nil {
			log.Println(err)
		}
		log.Println(string(respDump))
	}
}

func readConfig(path string, v interface{}) error {
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		data, err := ioutil.ReadFile(path)
		if err != nil {
			return fmt.Errorf("Failed to read file at %s: %v", path, err)
		}
		if err := json.NewDecoder(bytes.NewBuffer(data)).Decode(v); err != nil {
			return fmt.Errorf("Failed to read JSON to buffer: %v", err)
		}
	}

	return nil
}

func writeConfig(path string, v interface{}) error {
	var b bytes.Buffer

	if err := json.NewEncoder(&b).Encode(v); err != nil {
		return fmt.Errorf("Failed to write JSON to buffer: %v", err)
	}

	var readWriteModePerm os.FileMode
	readWriteModePerm = 0666
	if err := ioutil.WriteFile(path, b.Bytes(), readWriteModePerm); err != nil {
		return fmt.Errorf("Failed to write buffer to file at %s: %v", path, err)
	}

	return nil
}

func (destCfg *Config) updateConfig(srcCfg Config) error {
	if srcCfg.Username != "" {
		destCfg.Username = srcCfg.Username
	}
	if srcCfg.Password != "" {
		destCfg.Password = srcCfg.Password
	}
	if srcCfg.Env != "" {
		destCfg.Env = srcCfg.Env
	}
	if srcCfg.Requirements != "" {
		destCfg.Requirements = srcCfg.Requirements
	}
	if srcCfg.Train != "" {
		destCfg.Train = srcCfg.Train
	}
	if srcCfg.DataDir != "" {
		destCfg.DataDir = srcCfg.DataDir
	}
	if srcCfg.Price != "" {
		destCfg.Price = srcCfg.Price
	}
	return nil
}
