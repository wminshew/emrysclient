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
		"Learn more at https://www.emrys.io",
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
	loginCmd.Flags().IntP("save", "s", 7, "Days until token received in response on successful login expires.")
	startCmd.Flags().StringP("config", "c", ".emrysminer", "Path to config file (don't include extension)")
	startCmd.Flags().StringSliceP("bid-rates", "b", []string{}, "Per device bid rates ($/hr) for mining jobs (required; may set 1 value for all devices, or 1 value per device)")
	startCmd.Flags().StringSliceP("devices", "d", []string{}, "Cuda devices to mine with on emrys (does not affect mining-command in .emrysminer.yaml). If blank, program will mine with all detected devices.")
	startCmd.Flags().StringP("mining-command", "m", "", "Mining command to execute between emrys jobs. Use $DEVICE as the devices flag within the mining command so emrys can toggle mining correctly per device.")
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
