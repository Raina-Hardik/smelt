package tui

import "github.com/charmbracelet/lipgloss"

// theme is the global style sheet for the TUI.
var theme = struct {
	Title      lipgloss.Style
	Subtitle   lipgloss.Style
	FileLabel  lipgloss.Style
	StatusDone lipgloss.Style
	StatusErr  lipgloss.Style
	StatusAct  lipgloss.Style
	StatusPend lipgloss.Style
	StatusCxl  lipgloss.Style
	Box        lipgloss.Style
	LogBox     lipgloss.Style
	Help       lipgloss.Style
}{
	Title: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FF6B6B")),

	Subtitle: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#888888")),

	FileLabel: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#DDDDDD")),

	StatusDone: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#4ECDC4")),

	StatusErr: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FF6B6B")),

	StatusAct: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFE66D")),

	StatusPend: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#555555")),

	StatusCxl: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#B0883B")),

	Box: lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#444444")).
		Padding(0, 1),

	LogBox: lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#333333")).
		Padding(0, 1).
		Foreground(lipgloss.Color("#777777")),

	Help: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#444444")),
}
