package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of emrys",
	Long:  `All software has versions. This is emrys's`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("emrys v0.1.0")
	},
}
