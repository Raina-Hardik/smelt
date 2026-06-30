package cmd

import (
	"fmt"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/ffmpeg"
	"github.com/Raina-Hardik/smelt/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the interactive transcoding TUI",
	Long: `Launch the interactive transcoding TUI. Opens instantly; scans the source
directory and builds the transcode plan in the background while showing a
"Scanning…" state. Once ready, an editable configure screen appears — adjust
codec, CRF, preset, hwaccel, and workers, and see the concrete encoder that
--hwaccel resolves to. Press enter to start; jobs then run in parallel with
live progress, worker status, and logs. Press q or Ctrl+C to cancel running
jobs cleanly and quit.`,
	RunE: runTUI,
}

func init() {
	addTranscodeFlags(tuiCmd)
	tuiCmd.PreRunE = bindTranscodeFlags
	rootCmd.AddCommand(tuiCmd)
}

func runTUI(cmd *cobra.Command, args []string) error {
	if err := ffmpeg.CheckDeps(); err != nil {
		return err
	}
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return err
	}

	database, err := openDB()
	if err != nil {
		return fmt.Errorf("open history db: %w", err)
	}
	if database != nil {
		defer func() { _ = database.Close() }()
	}

	model := tui.New(cfg, cmd.Context(), database)
	prog := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(cmd.Context()))
	_, err = prog.Run()
	return err
}
