package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/wminshew/emrysclient/cmd/login"
	"github.com/wminshew/emrysclient/cmd/mine"
	"github.com/wminshew/emrysclient/cmd/run"
	"github.com/wminshew/emrysclient/cmd/update"
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
		"user models." +
		"\n\nLearn more at https://www.emrys.io, and please report" +
		"bugs to support@emrys.io",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Use \"emrys --help\" for more information about subcommands.\n")
	},
}

func init() {
	rootCmd.AddCommand(version.Cmd)
	rootCmd.AddCommand(login.Cmd)
	rootCmd.AddCommand(run.Cmd)
	rootCmd.AddCommand(mine.Cmd)
	rootCmd.AddCommand(update.Cmd)
}

// Execute the root command
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
