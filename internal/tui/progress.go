package tui

import (
	"fmt"
	"path/filepath"
	"time"

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
	eta := theme.StatusPend.Render(fmt.Sprintf(" eta %s", formatETA(fi.eta)))
	return lipgloss.JoinHorizontal(lipgloss.Top, label, fi.prog.View(), " ", pct, eta)
}

// formatETA renders a remaining-time estimate as "12s" / "3m12s" / "1h05m", or
// "--" while ffmpeg hasn't reported a speed yet (e.g. the first few frames).
func formatETA(d time.Duration) string {
	if d <= 0 {
		return "--"
	}
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
