package cmd

import (
	"fmt"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/scanner"
	"github.com/Raina-Hardik/smelt/internal/tui"
	"github.com/Raina-Hardik/smelt/internal/worker"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the interactive transcoding TUI",
	Long: `Launch the interactive transcoding TUI. Scans the source directory and opens an
editable configure screen — adjust codec, CRF, preset, hwaccel, and workers, and
see the concrete encoder that --hwaccel resolves to. Press enter to start; jobs
then run in parallel with live progress, worker status, and logs. Press q or
Ctrl+C to cancel running jobs cleanly and quit.`,
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

	database, err := openDB()
	if err != nil {
		return fmt.Errorf("open history db: %w", err)
	}
	if database != nil {
		defer database.Close()
	}

	files, err := scanner.Scan(cfg.Src, cfg.Ext)
	if err != nil {
		return fmt.Errorf("scan %s: %w", cfg.Src, err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no files matching %v under %s", cfg.Ext, cfg.Src)
	}

	files, _ = worker.Plan(cmd.Context(), files, cfg, database)
	if len(files) == 0 {
		return fmt.Errorf("nothing to transcode; files are already up to date (use --force)")
	}

	// Confirm the destructive --inplace path before taking over the screen.
	if ok, err := confirmInplace(cfg, len(files)); err != nil {
		return err
	} else if !ok {
		return nil
	}

	model := tui.New(cfg, files, cmd.Context(), database)
	prog := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(cmd.Context()))
	_, err = prog.Run()
	return err
}
