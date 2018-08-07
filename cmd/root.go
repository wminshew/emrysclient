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
	Long: "An easy & cost-effective deep learning training " +
		"dispatcher. Emrys lets you quickly train models " +
		"wherever its safe & most cost effective.\n" +
		"Learn more at https://emrys.io",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Use \"emrys --help\" for more information about subcommands.\n")
	},
}

func init() {
	// cobra.OnInitialize(initConfig)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(registerCmd)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(runCmd)
	loginCmd.Flags().Int("save", 7, "Days until token received in response on successful login expires.")
	runCmd.Flags().String("config", ".emrys", "Path to config file (don't include extension)")
	runCmd.Flags().String("project", "", "User project (required)")
	runCmd.Flags().String("requirements", "", "Path to requirements file (required)")
	runCmd.Flags().String("main", "", "Path to main execution file (required)")
	runCmd.Flags().String("data", "", "Path to the data directory (optional)")
	runCmd.Flags().String("output", "", "Path to save the output directory (required)")
	runCmd.Flags().SortFlags = false
	if err := func() error {
		if err := viper.BindPFlag("save", loginCmd.Flags().Lookup("save")); err != nil {
			return err
		}
		if err := viper.BindPFlag("config", runCmd.Flags().Lookup("config")); err != nil {
			return err
		}
		if err := viper.BindPFlag("requirements", runCmd.Flags().Lookup("requirements")); err != nil {
			return err
		}
		if err := viper.BindPFlag("main", runCmd.Flags().Lookup("main")); err != nil {
			return err
		}
		if err := viper.BindPFlag("data", runCmd.Flags().Lookup("data")); err != nil {
			return err
		}
		if err := viper.BindPFlag("output", runCmd.Flags().Lookup("output")); err != nil {
			return err
		}
		return nil
	}(); err != nil {
		log.Printf("Error binding pflag: %v\n", err)
		panic(err)
	}
}

// Execute the root command
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
