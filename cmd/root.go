package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"log"
	"os"
)

var rootCmd = &cobra.Command{
	Use:   "emrys",
	Short: "Emrys is an aggregator for GPU compute",
	Long: `An easy & cost-effective deep learning training
dispatcher. Emrys lets you quickly train models
wherever its safest & most cost effective.

Learn more at https://emrys.io`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Use \"emrys --help\" for more information about subcommands.\n")
	},
}

func init() {
	// cobra.OnInitialize(initConfig)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().String("config", ".emrys", "Path to your config file for training (don't include extension)")
	runCmd.Flags().String("requirements", "./requirements.txt", "Path to your requirements file for training")
	runCmd.Flags().String("train", "./train.py", "Path to your training file")
	runCmd.Flags().String("data", "./data", "Path to the directory holding your data")
	runCmd.Flags().SortFlags = false
	err := viper.BindPFlag("config", runCmd.Flags().Lookup("config"))
	if err != nil {
		log.Fatalf("Error binding pflag config")
	}
	err = viper.BindPFlag("requirements", runCmd.Flags().Lookup("requirements"))
	if err != nil {
		log.Fatalf("Error binding pflag requirements")
	}
	err = viper.BindPFlag("train", runCmd.Flags().Lookup("train"))
	if err != nil {
		log.Fatalf("Error binding pflag train")
	}
	err = viper.BindPFlag("data", runCmd.Flags().Lookup("data"))
	if err != nil {
		log.Fatalf("Error binding pflag data")
	}
}

// Execute the root command
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
