package cmd

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/scanner"
	smeltui "github.com/Raina-Hardik/smelt/internal/tui"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the interactive transcoding TUI",
	RunE:  runTUI,
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}

func runTUI(cmd *cobra.Command, args []string) error {
	cfg := config.Load()

	files, err := scanner.Scan(cfg.Src, cfg.Extensions)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	model := smeltui.New(cfg, files, cmd.Context())
	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err = p.Run()
	return err
}
