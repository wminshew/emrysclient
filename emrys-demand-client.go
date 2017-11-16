package main

import (
	// "context"
	"flag"
	"fmt"
	"os"
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
	var envPtr string
	var trainPtr string
	// TODO: create own struct implementing Value interface for price
	var pricePtr string

	// cfg subcommand flag pointers
	cfgCmd.StringVar(&envPtr, "env", "tensorflow:latest", "Environment to execute within {tensorflow:latest, pytorch:latest}.")
	cfgCmd.StringVar(&trainPtr, "train", "", "Code to execute. (required)")
	// TODO: create own struct implementing Value interface for price
	cfgCmd.StringVar(&pricePtr, "price", "", "Maximum acceptable price per calc. (required)")

	// run subcommand flag pointers
	runCmd.StringVar(&envPtr, "env", "tensorflow:latest", "Environment to execute within {tensorflow:latest, pytorch:latest}.")
	runCmd.StringVar(&trainPtr, "train", "", "Code to execute. (required)")
	// TODO: create own struct implementing Value interface for price
	runCmd.StringVar(&pricePtr, "price", "", "Maximum acceptable price per calc. (required)")

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

	// global flag handling

	// required flags
	if trainPtr == "" || pricePtr == "" {
		cfgCmd.PrintDefaults()
		os.Exit(1)
	}

	// choice flags
	envChoices := map[string]bool{"tensorflow:latest": true, "pytorch:latest": true}
	if _, validChoices := envChoices[envPtr]; !validChoices {
		cfgCmd.PrintDefaults()
		os.Exit(1)
	}

	// executed parsed subcommand
	if cfgCmd.Parsed() {
		// config subcommand flag handling

		fmt.Printf("env: %s, train: %s, price: %s\n",
			envPtr, trainPtr, pricePtr)
	}

	if runCmd.Parsed() {
		// run subcommand flag handling

		fmt.Printf("env: %s, train: %s, price: %s\n",
			envPtr, trainPtr, pricePtr)
	}
}
