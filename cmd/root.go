package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wminshew/emrysclient/cmd/login"
	"github.com/wminshew/emrysclient/cmd/run"
	"github.com/wminshew/emrysclient/cmd/version"
	"log"
	"os"
)

var rootCmd = &cobra.Command{
	Use:   "emrys",
	Short: "Emrys is an aggregator for GPU compute",
	Long: "An easy & effective serverless deep learning client. " +
		"Emrys run lets you quickly train models wherever its safe " +
		"& most cost effective.\n" +
		"Emrys mine lets you earn money with idle GPUs by training " +
		"user models.\n" +
		"Learn more at https://www.emrys.io",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Use \"emrys --help\" for more information about subcommands.\n")
	},
}

func init() {
	rootCmd.AddCommand(version.Cmd)
	rootCmd.AddCommand(login.Cmd)
	rootCmd.AddCommand(run.Cmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(startCmd)
	loginCmd.Flags().IntP("save", "s", 7, "Days until token received in response on successful login expires.")
	startCmd.Flags().StringP("config", "c", ".emrysminer", "Path to config file (don't include extension)")
	startCmd.Flags().StringSliceP("bid-rates", "b", []string{}, "Per device bid rates ($/hr) for mining jobs (required; may set 1 value for all devices, or 1 value per device)")
	startCmd.Flags().StringSliceP("devices", "d", []string{}, "Cuda devices to mine with on emrys. If blank, program will mine with all detected devices.")
	startCmd.Flags().StringP("mining-command", "m", "", "Mining command to execute between emrys jobs. Must use $DEVICE flag so emrys can toggle mining-per-device correctly between jobs.")
	startCmd.Flags().SortFlags = false
	if err := viper.BindPFlag("save", loginCmd.Flags().Lookup("save")); err != nil {
		log.Printf("Error binding pflag config")
		return
	}
	if err := viper.BindPFlag("config", startCmd.Flags().Lookup("config")); err != nil {
		log.Printf("Error binding pflag config")
		return
	}
	if err := viper.BindPFlag("bid-rates", startCmd.Flags().Lookup("bid-rates")); err != nil {
		log.Printf("Error binding pflag bid-rate")
		return
	}
	if err := viper.BindPFlag("devices", startCmd.Flags().Lookup("devices")); err != nil {
		log.Printf("Error binding pflag devices")
		return
	}
	if err := viper.BindPFlag("mining-command", startCmd.Flags().Lookup("mining-command")); err != nil {
		log.Printf("Error binding pflag config")
		return
	}
}

// Execute the root command
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
