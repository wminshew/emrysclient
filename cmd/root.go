package cmd

import (
	"fmt"
	"net/url"
	"os"

	"github.com/spf13/cobra"
	// "github.com/spf13/viper"
)

// baseURL := "https://localhost:8080"
// TODO: might have to get new certificate for server for this URL
// and update cURLs
// TODO: test different ports and http vs https
// var baseURL, _ = url.Parse("https://wmdlserver.ddns.net:4430")
var baseURL, _ = url.Parse("https://localhost:4430")

var rootCmd = &cobra.Command{
	Use:   "emrys",
	Short: "Emrys is an aggregator for GPU compute",
	Long: `An easy & effective deep learning training
	dispatcher. Emrys lets you quickly train models
	wherever its most cost effective.
	Learn more at https://emrys.io`,
	Run: func(cmd *cobra.Command, args []string) {

	},
}

func init() {
	cobra.OnInitialize(initConfig)

	// TODO: set version
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(runCmd)
}

func initConfig() {

}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
