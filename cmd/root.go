package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"log"
	"os"
)

var rootCmd = &cobra.Command{
	Use:   "emrysminer",
	Short: "Emrys is an aggregator for GPU compute",
	Long: "An easy & effective deep learning miner. Emrysminer " +
		"lets you earn money while training user models.\n" +
		"Learn more at https://emrys.io",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Use \"emrysminer --help\" for more information about subcommands.\n")
	},
}

func init() {
	// cobra.OnInitialize(initConfig)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(startCmd)
	loginCmd.Flags().Int("save", 7, "Days until token received in response on successful login expires.")
	startCmd.Flags().String("config", ".emrysminer", "Path to config file (don't include extension)")
	startCmd.Flags().Float64("bid-rate", 0, "Bid rate ($/hr) for mining jobs (required)")
	startCmd.Flags().SortFlags = false
	err := viper.BindPFlag("save", loginCmd.Flags().Lookup("save"))
	if err != nil {
		log.Fatalf("Error binding pflag config")
	}
	err = viper.BindPFlag("config", startCmd.Flags().Lookup("config"))
	if err != nil {
		log.Fatalf("Error binding pflag config")
	}
	err = viper.BindPFlag("bid-rate", startCmd.Flags().Lookup("bid-rate"))
	if err != nil {
		log.Fatalf("Error binding pflag bid-rate")
	}
}

// Execute the root command
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
