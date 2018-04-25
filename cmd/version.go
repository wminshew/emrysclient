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
		// TODO: decide on a versioning scheme; likely semver
		fmt.Println("emrys v0.1")
	},
}
