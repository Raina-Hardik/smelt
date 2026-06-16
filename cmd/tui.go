package cmd

import (
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the interactive transcoding TUI",
	Long: `Launch the interactive transcoding TUI. Scans the source directory and begins
transcoding in parallel. Progress, worker status, and logs are displayed live.
Press q or Ctrl+C to quit; active jobs are cancelled cleanly.`,
	RunE: runTUI,
}

func init() {
	addTranscodeFlags(tuiCmd)
	rootCmd.AddCommand(tuiCmd)
}

func runTUI(cmd *cobra.Command, args []string) error {
	log.Info().Str("cmd", "tui").Msg("not implemented")
	return nil
}
