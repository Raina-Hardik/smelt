package cmd

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// version is overridden by CI via -ldflags="-X github.com/Raina-Hardik/smelt/cmd.version=v1.0.0".
// For go install builds the module version stamped by the toolchain is used instead.
var version = ""

func resolvedVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print smelt version information",
	Long:  "Print smelt version information.",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("smelt %s (%s, %s/%s)\n", resolvedVersion(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
