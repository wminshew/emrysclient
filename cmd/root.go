package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/wminshew/emrysuser/cmd/login"
	"github.com/wminshew/emrysuser/cmd/run"
	"github.com/wminshew/emrysuser/cmd/version"
	"log"
	"os"
)

var rootCmd = &cobra.Command{
	Use:   "emrys",
	Short: "Emrys is an aggregator for GPU compute",
	Long: "An easy & cost-effective deep learning training " +
		"dispatcher. Emrys lets you quickly train models " +
		"wherever its safe & most cost effective.\n" +
		"Learn more at https://www.emrys.io",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Use \"emrys --help\" for more information about subcommands.\n")
	},
}

func init() {
	rootCmd.AddCommand(version.Cmd)
	rootCmd.AddCommand(login.Cmd)
	rootCmd.AddCommand(run.Cmd)
}

// Execute the root command
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
