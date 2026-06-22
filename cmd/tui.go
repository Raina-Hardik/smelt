package cmd

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/scanner"
	"github.com/Raina-Hardik/smelt/internal/tui"
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
	tuiCmd.PreRunE = bindTranscodeFlags
	rootCmd.AddCommand(tuiCmd)
}

func runTUI(cmd *cobra.Command, args []string) error {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return err
	}

	files, err := scanner.Scan(cfg.Src, cfg.Ext)
	if err != nil {
		return fmt.Errorf("scan %s: %w", cfg.Src, err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no files matching %v under %s", cfg.Ext, cfg.Src)
	}

	// Confirm the destructive --inplace path before taking over the screen.
	if ok, err := confirmInplace(cfg, len(files)); err != nil {
		return err
	} else if !ok {
		return nil
	}

	model := tui.New(cfg, files, cmd.Context())
	prog := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(cmd.Context()))
	_, err = prog.Run()
	return err
}
