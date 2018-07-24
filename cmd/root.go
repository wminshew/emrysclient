package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
	rootCmd.AddCommand(registerCmd)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(startCmd)
	loginCmd.Flags().Int("save", 7, "Days until token received in response on successful login expires.")
	startCmd.Flags().String("config", ".emrysminer", "Path to config file (don't include extension)")
	startCmd.Flags().Float64("bid-rate", 0, "Bid rate ($/hr) for mining jobs (required)")
	startCmd.Flags().SortFlags = false
	if err := viper.BindPFlag("save", loginCmd.Flags().Lookup("save")); err != nil {
		fmt.Printf("Error binding pflag config")
		return
	}
	if err := viper.BindPFlag("config", startCmd.Flags().Lookup("config")); err != nil {
		fmt.Printf("Error binding pflag config")
		return
	}
	if err := viper.BindPFlag("bid-rate", startCmd.Flags().Lookup("bid-rate")); err != nil {
		fmt.Printf("Error binding pflag bid-rate")
		return
	}
}

// Execute the root command
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
