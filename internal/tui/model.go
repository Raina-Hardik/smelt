package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/ffmpeg"
	"github.com/Raina-Hardik/smelt/internal/scanner"
	"github.com/Raina-Hardik/smelt/internal/worker"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── message types ────────────────────────────────────────────────────────────

type progressMsg struct{ ev ffmpeg.ProgressEvent }
type completeMsg struct {
	file scanner.MediaFile
	err  error
}
type allDoneMsg struct{}

// ── file item ────────────────────────────────────────────────────────────────

type fileStatus int

const (
	statusPending fileStatus = iota
	statusActive
	statusDone
	statusError
)

type fileItem struct {
	file    scanner.MediaFile
	status  fileStatus
	percent float64
	prog    progress.Model
}

// listEntry implements list.Item (and list.DefaultItem via Title/Description).
type listEntry struct {
	name   string
	status fileStatus
}

func (le listEntry) FilterValue() string { return le.name }
func (le listEntry) Title() string       { return le.name }
func (le listEntry) Description() string {
	switch le.status {
	case statusActive:
		return "transcoding"
	case statusDone:
		return "done"
	case statusError:
		return "error"
	default:
		return "pending"
	}
}

// ── model ────────────────────────────────────────────────────────────────────

type Model struct {
	cfg       *config.Config
	pool      *worker.Pool
	files     []scanner.MediaFile
	fileItems []fileItem
	fileIndex map[string]int // path → index in fileItems
	list      list.Model
	logs      []string
	events    chan tea.Msg
	ctx       context.Context
	done      int
	errCount  int
	width     int
	height    int
	quitting  bool
}

func New(cfg *config.Config, files []scanner.MediaFile, ctx context.Context) Model {
	pool := worker.New(cfg)
	events := make(chan tea.Msg, len(files)*4+4)

	fileItems := make([]fileItem, len(files))
	fileIndex := make(map[string]int, len(files))
	listItems := make([]list.Item, len(files))

	for i, f := range files {
		name := filepath.Base(f.Path)
		fileItems[i] = fileItem{
			file:   f,
			status: statusPending,
			prog:   newProgressBar(),
		}
		fileIndex[f.Path] = i
		listItems[i] = listEntry{name: name, status: statusPending}
	}

	l := list.New(listItems, list.NewDefaultDelegate(), 80, 12)
	l.Title = "file queue"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)

	return Model{
		cfg:       cfg,
		pool:      pool,
		files:     files,
		fileItems: fileItems,
		fileIndex: fileIndex,
		list:      l,
		events:    events,
		ctx:       ctx,
	}
}

// ── bubbletea interface ───────────────────────────────────────────────────────

func (m Model) Init() tea.Cmd {
	go func() {
		m.pool.RunWithCallbacks(m.ctx, m.files,
			func(ev ffmpeg.ProgressEvent) { m.events <- progressMsg{ev: ev} },
			func(f scanner.MediaFile, err error) { m.events <- completeMsg{file: f, err: err} },
		)
		m.events <- allDoneMsg{}
	}()
	return listenForEvent(m.events)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		listH := m.height/2 - 2
		if listH < 4 {
			listH = 4
		}
		m.list.SetSize(m.width-4, listH)
		return m, nil

	case progressMsg:
		idx, ok := m.fileIndex[msg.ev.FilePath]
		if !ok {
			return m, listenForEvent(m.events)
		}
		m.fileItems[idx].status = statusActive
		m.fileItems[idx].percent = msg.ev.Percent
		// Update list entry status
		m.list.SetItem(idx, listEntry{
			name:   filepath.Base(msg.ev.FilePath),
			status: statusActive,
		})
		setCmd := m.fileItems[idx].prog.SetPercent(msg.ev.Percent)
		return m, tea.Batch(setCmd, listenForEvent(m.events))

	case completeMsg:
		idx, ok := m.fileIndex[msg.file.Path]
		if !ok {
			return m, listenForEvent(m.events)
		}
		name := filepath.Base(msg.file.Path)
		if msg.err != nil {
			m.fileItems[idx].status = statusError
			m.errCount++
			m.logs = append(m.logs, fmt.Sprintf("✗ %s: %v", name, msg.err))
			m.list.SetItem(idx, listEntry{name: name, status: statusError})
		} else {
			m.fileItems[idx].status = statusDone
			m.fileItems[idx].percent = 1
			_ = m.fileItems[idx].prog.SetPercent(1)
			m.done++
			m.logs = append(m.logs, fmt.Sprintf("✓ %s", name))
			m.list.SetItem(idx, listEntry{name: name, status: statusDone})
		}
		return m, listenForEvent(m.events)

	case allDoneMsg:
		m.logs = append(m.logs, fmt.Sprintf("all done — %d ok, %d failed", m.done, m.errCount))
		return m, nil

	case progress.FrameMsg:
		var cmds []tea.Cmd
		for i := range m.fileItems {
			if m.fileItems[i].status == statusActive {
				pm, cmd := m.fileItems[i].prog.Update(msg)
				if p, ok := pm.(progress.Model); ok {
					m.fileItems[i].prog = p
				}
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.width == 0 {
		return "initializing…\n"
	}

	workerStr := fmt.Sprintf("workers: %d", m.cfg.Workers)
	statsStr := fmt.Sprintf("files: %d/%d  errors: %d  %s",
		m.done, len(m.files), m.errCount, workerStr)

	header := lipgloss.JoinHorizontal(lipgloss.Top,
		theme.Title.Render("⚡ smelt"),
		"  ",
		theme.Subtitle.Render(statsStr),
	)

	sections := []string{header, "", theme.Box.Render(m.list.View()), ""}

	if active := m.renderActiveFiles(); active != "" {
		sections = append(sections, theme.Box.Render(active), "")
	}

	sections = append(sections, theme.LogBox.Render(m.renderLogs()))
	sections = append(sections, theme.Help.Render("q quit"))

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m Model) renderActiveFiles() string {
	var lines []string
	for _, fi := range m.fileItems {
		if fi.status == statusActive {
			lines = append(lines, renderActiveFile(fi))
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderLogs() string {
	logs := m.logs
	if len(logs) > 8 {
		logs = logs[len(logs)-8:]
	}
	if len(logs) == 0 {
		return "waiting for workers…"
	}
	return strings.Join(logs, "\n")
}

// listenForEvent returns a Cmd that blocks until the next event arrives on ch.
func listenForEvent(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}
