package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	// "context"
)

func main() {
	// return useful info for help flags
	flag.Usage = func() {
		fmt.Printf("Usage of %s:\n", os.Args[0])
		fmt.Printf("  config or run subcommand is required\n")
		flag.PrintDefaults()
	}
	flag.Parse()

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

	// cfg subcommand flag pointers
	cfgCmd.StringVar(&usernamePtr, "username", "", "Set the local or global username.")
	cfgCmd.StringVar(&passwordPtr, "password", "", "Set the local or global password.")
	cfgCmd.StringVar(&envPtr, "env", "", "Set the local or global environment.")
	cfgCmd.StringVar(&trainPtr, "train", "", "Set the local or global default train path.")
	// TODO: create own struct implementing Value interface for price
	cfgCmd.StringVar(&pricePtr, "price", "", "Set the local or global price per calc.")

	// run subcommand flag pointers
	runCmd.StringVar(&usernamePtr, "username", "", "Username flag overrides local and global config settings. (required if not set in config)")
	runCmd.StringVar(&passwordPtr, "password", "", "Password flag overrides local and global config settings. (required if not set in config)")
	runCmd.StringVar(&envPtr, "env", "tensorflow:latest", "Environment to execute within {tensorflow:latest, pytorch:latest}.")
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

		fmt.Printf("env: %s, train: %s, price: %s\n",
			envPtr, trainPtr, pricePtr)
	}

	if runCmd.Parsed() {
		// run subcommand flag handling

		// required flags
		if usernamePtr == "" || passwordPtr == "" || trainPtr == "" || pricePtr == "" {
			runCmd.PrintDefaults()
			os.Exit(1)
		}

		// choice flags
		envChoices := map[string]bool{"tensorflow:latest": true, "pytorch:latest": true}
		if _, validChoices := envChoices[envPtr]; !validChoices {
			runCmd.PrintDefaults()
			os.Exit(1)
		}

		fmt.Printf("env: %s, train: %s, price: %s\n",
			envPtr, trainPtr, pricePtr)

		url := "http://127.0.0.1:8080/"
		client := &http.Client{}
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			log.Fatalln(err)
		}
		req.SetBasicAuth(usernamePtr, passwordPtr)
		resp, err := client.Do(req)
		if err != nil {
			log.Fatalln(err)
		}
		defer resp.Body.Close()
		body, err := ioutil.ReadAll(resp.Body)
		fmt.Printf("%s\n", body)

	}
}
