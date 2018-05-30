package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of emrysminer",
	Long:  `All software has versions. This is emrysminer's`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("emrysminer v0.1.0")
	},
}
