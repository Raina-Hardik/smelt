package tui

import (
	"fmt"
	"path/filepath"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
)

const progressBarWidth = 38

func newProgressBar() progress.Model {
	return progress.New(
		progress.WithDefaultGradient(),
		progress.WithWidth(progressBarWidth),
	)
}

// renderActiveFile renders a single active file's label + progress bar inline.
func renderActiveFile(fi fileItem) string {
	name := filepath.Base(fi.file.Path)
	if len(name) > 30 {
		name = "…" + name[len(name)-29:]
	}
	label := theme.FileLabel.Render(fmt.Sprintf("%-31s", name))
	pct := theme.StatusAct.Render(fmt.Sprintf("%3.0f%%", fi.percent*100))
	return lipgloss.JoinHorizontal(lipgloss.Top, label, fi.prog.View(), " ", pct)
}
