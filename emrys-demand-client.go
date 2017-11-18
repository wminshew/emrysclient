package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"gopkg.in/mattes/go-expand-tilde.v1"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	// "context"
)

type Config struct {
	Username string `json:"Username,omitempty"`
	Password string `json:"Password,omitempty"`
	Env      string `json:"Env,omitempty"`
	Train    string `json:"Train,omitempty"`
	Price    string `json:"Price,omitempty"`
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
	cfg := Config{}

	// subcommands
	cfgCmd := flag.NewFlagSet("config", flag.ExitOnError)
	runCmd := flag.NewFlagSet("run", flag.ExitOnError)

	// global flags shared by subcommands
	var usernamePtr string
	var passwordPtr string
	var envPtr string
	var trainPtr string
	// TODO: create own struct implementing Value interface for price
	var pricePtr string
	var localCfg bool
	var globalCfg bool

	// cfg subcommand flag pointers
	cfgCmd.StringVar(&usernamePtr, "username", "", "Set the local or global username.")
	cfgCmd.StringVar(&passwordPtr, "password", "", "Set the local or global password.")
	cfgCmd.StringVar(&envPtr, "env", "", "Set the local or global environment.")
	cfgCmd.StringVar(&trainPtr, "train", "", "Set the local or global default train path.")
	// TODO: create own struct implementing Value interface for price
	cfgCmd.StringVar(&pricePtr, "price", "", "Set the local or global price per calc.")
	cfgCmd.BoolVar(&localCfg, "local", false, "Save config locally (saved in this directory).")
	cfgCmd.BoolVar(&globalCfg, "global", false, "Save config globally (saved in home directory).")

	// run subcommand flag pointers
	runCmd.StringVar(&usernamePtr, "username", "", "Username flag overrides local and global config settings. (required if not set in config)")
	runCmd.StringVar(&passwordPtr, "password", "", "Password flag overrides local and global config settings. (required if not set in config)")
	runCmd.StringVar(&envPtr, "env", "", "Environment to execute within {tensorflow:latest, pytorch:latest}.")
	runCmd.StringVar(&trainPtr, "train", "", "Code to execute. (required if not set in config)")
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

	// load global config
	// load local config (override if necessary)
	// load flags (override if necessary)

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
		if trainPtr != "" {
			cfg.Train = trainPtr
		}
		if pricePtr != "" {
			cfg.Price = pricePtr
		}
		fmt.Printf("Config: %v\n", cfg)

		if localCfg {
			var localCfg Config
			pwd, _ := os.Getwd()
			path := pwd + "/.emrysconfig"

			// read the existing local config file
			if err := ReadConfig(path, &localCfg); err != nil {
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
			if err := ReadConfig(path, &globalCfg); err != nil {
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
			fmt.Printf("Writing to current working directory...\n")
			if err := WriteConfig(path, &globalCfg); err != nil {
				log.Printf("Failed to save global config file: %v\n", err)
			} else {
				fmt.Printf("Success! Wrote to %s\n", path)
			}

			fmt.Printf("Writing to home directory...\n")
			if err := WriteConfig(path, &cfg); err != nil {
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
		if err := ReadConfig(path, &globalCfg); err != nil {
			log.Printf("Error reading global config file: %v\n", err)
		}
		cfg.updateConfig(globalCfg)

		var localCfg Config
		pwd, _ := os.Getwd()
		path = pwd + "/.emrysconfig"

		// read the existing local config file and override any global settings
		if err := ReadConfig(path, &localCfg); err != nil {
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
		if trainPtr != "" {
			cfg.Train = trainPtr
		}
		if pricePtr != "" {
			cfg.Price = pricePtr
		}
		fmt.Printf("Config: %v\n", cfg)

		// required flags
		// TODO: replace with ValidForRun() call on config or something..
		if cfg.Username == "" || cfg.Password == "" || cfg.Train == "" || cfg.Price == "" {
			runCmd.PrintDefaults()
			os.Exit(1)
		}

		// choice flags
		envChoices := map[string]bool{"tensorflow:latest": true, "pytorch:latest": true}
		if _, validChoices := envChoices[cfg.Env]; !validChoices {
			runCmd.PrintDefaults()
			os.Exit(1)
		}

		url := "http://127.0.0.1:8080/"
		client := &http.Client{}
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			log.Fatalln(err)
		}
		req.SetBasicAuth(cfg.Username, cfg.Password)
		resp, err := client.Do(req)
		if err != nil {
			log.Fatalln(err)
		}
		defer resp.Body.Close()
		body, err := ioutil.ReadAll(resp.Body)
		fmt.Printf("%s\n", body)

	}
}

func ReadConfig(path string, v interface{}) error {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return fmt.Errorf("Failed to read file at %s: %v", path, err)
	}
	if err := json.NewDecoder(bytes.NewBuffer(data)).Decode(v); err != nil {
		return fmt.Errorf("Failed to  JSON to buffer: %v", err)
	}

	return nil
}

func WriteConfig(path string, v interface{}) error {
	var b bytes.Buffer

	if err := json.NewEncoder(&b).Encode(v); err != nil {
		return fmt.Errorf("Failed to write JSON to buffer: %v", err)
	}

	var readWriteModePerm os.FileMode
	readWriteModePerm = 0666
	fmt.Printf("%v\n", readWriteModePerm)
	if err := ioutil.WriteFile(path, b.Bytes(), readWriteModePerm); err != nil {
		return fmt.Errorf("Failed to write buffer to file at %s: %v", path, err)
	}

	return nil
}

func (cfg *Config) updateConfig(srcCfg Config) error {
	if srcCfg.Username != "" {
		cfg.Username = srcCfg.Username
	}
	if srcCfg.Password != "" {
		cfg.Password = srcCfg.Password
	}
	if srcCfg.Env != "" {
		cfg.Env = srcCfg.Env
	}
	if srcCfg.Train != "" {
		cfg.Train = srcCfg.Train
	}
	if srcCfg.Price != "" {
		cfg.Price = srcCfg.Price
	}
	return nil
}
