package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// version is set at build time via -ldflags="-X github.com/Raina-Hardik/smelt/cmd.version=v1.0.0".
var version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print smelt version information",
	Long:  "Print smelt version information.",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("smelt %s (%s, %s/%s)\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
