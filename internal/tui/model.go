package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/db"
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

// scanDoneMsg carries the result of the background scan+plan.
type scanDoneMsg struct {
	files   []scanner.MediaFile
	skipped int
	blocked int
	err     error
}

// resolvedMsg carries the concrete encoder/backend the pre-flight probe picked,
// tagged with the codec/hwaccel it was probed for so a stale result (from a
// since-changed setting) can be ignored.
type resolvedMsg struct{ encoder, backend, codec, hwaccel string }

// ── editable pre-flight fields ─────────────────────────────────────────────────

type confField int

const (
	fCodec confField = iota
	fCRF
	fPreset
	fHWAccel
	fHWDecode
	fWorkers
	fDecodeThreads
	fAudioCodec
	fAudioBitrate
	fSubs
	fInPlace
	confFieldCount
)

var (
	codecChoices        = []string{"h264", "h265", "av1", "vp9"}
	hwaccelChoices      = []string{"auto", "none", "nvenc", "qsv", "vaapi", "amf", "videotoolbox"}
	hwdecodeChoices     = []string{"auto", "off"}
	audioCodecChoices   = ffmpeg.KnownAudioCodecs()
	audioBitrateChoices = []string{"", "96k", "128k", "192k", "256k", "320k"}
	subsChoices         = []string{"copy", "drop"}
)

// ── file item ────────────────────────────────────────────────────────────────

type fileStatus int

const (
	statusPending fileStatus = iota
	statusActive
	statusDone
	statusError
	statusCancelled
)

// rendering budgets: bound the active-worker and log panels so the layout fits
// short terminals; the file queue takes whatever height is left.
const (
	maxActiveRows = 6
	maxLogLines   = 8
)

func (s fileStatus) label() string {
	switch s {
	case statusActive:
		return "transcoding"
	case statusDone:
		return "done"
	case statusError:
		return "error"
	case statusCancelled:
		return "cancelled"
	default:
		return "pending"
	}
}

type fileItem struct {
	file    scanner.MediaFile
	status  fileStatus
	percent float64
	eta     time.Duration
	prog    progress.Model
}

// listEntry implements list.Item (and list.DefaultItem via Title/Description).
type listEntry struct {
	name   string
	status fileStatus
}

func (le listEntry) FilterValue() string { return le.name }
func (le listEntry) Title() string       { return le.name }
func (le listEntry) Description() string { return le.status.label() }

// ── model ────────────────────────────────────────────────────────────────────

type Model struct {
	cfg        *config.Config
	db         *db.DB       // nil when DB is disabled
	pool       *worker.Pool // created when the run starts; nil on the pre-flight screen
	field      confField    // focused field on the editable pre-flight screen
	files      []scanner.MediaFile
	fileItems  []fileItem
	fileIndex  map[string]int // path → index in fileItems
	list       list.Model
	logs       []string
	events     chan tea.Msg
	ctx        context.Context
	cancel     context.CancelFunc
	encoder    string // resolved concrete encoder, for the pre-flight screen
	backend    string // resolved hw backend ("" = software)
	resolved   bool   // the encoder probe has returned
	done       int
	errCount   int
	width      int
	height     int
	scanning   bool  // scan+plan running in background; pre-flight not shown yet
	scanErr    error // non-nil if scan failed; shows error screen
	started    bool  // user confirmed the pre-flight screen; pool is running
	quitting   bool
	cancelling bool // q/Ctrl+C pressed: draining in-flight work before exit
	finished   bool // the worker pool drained on its own (allDoneMsg seen)
	paused     bool // dispatch paused via p (in-flight jobs keep running)
	showHelp   bool
	confirming bool // --inplace + !AssumeYes: pre-flight enter shows a confirm prompt instead of starting
}

// New creates a TUI model that opens immediately in a "Scanning…" state.
// The scan and plan run as a background tea.Cmd via Init so the terminal is
// taken over before any blocking filesystem walk begins.
func New(cfg *config.Config, ctx context.Context, database *db.DB) Model {
	// Own a cancellable child so q/Q can cancel in-flight ffmpeg children.
	ctx, cancel := context.WithCancel(ctx)

	l := list.New(nil, list.NewDefaultDelegate(), 80, 12)
	l.Title = "file queue"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.SetShowHelp(false)

	return Model{
		cfg:      cfg,
		db:       database,
		list:     l,
		ctx:      ctx,
		cancel:   cancel,
		scanning: true,
	}
}

// initFileItems populates the file list once the scan completes. Called from
// Update on scanDoneMsg; safe because the pool has not started yet.
func (m *Model) initFileItems(files []scanner.MediaFile) {
	m.files = files
	m.events = make(chan tea.Msg, len(files)*4+4)
	m.fileItems = make([]fileItem, len(files))
	m.fileIndex = make(map[string]int, len(files))
	listItems := make([]list.Item, len(files))
	for i, f := range files {
		name := filepath.Base(f.Path)
		m.fileItems[i] = fileItem{file: f, status: statusPending, prog: newProgressBar()}
		m.fileIndex[f.Path] = i
		listItems[i] = listEntry{name: name, status: statusPending}
	}
	m.list.SetItems(listItems)
}

// ── bubbletea interface ───────────────────────────────────────────────────────

func (m Model) Init() tea.Cmd {
	// Both commands run concurrently off the UI goroutine. The scan+plan result
	// arrives as scanDoneMsg; the encoder probe as resolvedMsg.
	return tea.Batch(m.resolveCmd(), m.scanCmd())
}

// scanCmd walks the source directory and builds the transcode plan off-thread.
func (m Model) scanCmd() tea.Cmd {
	cfg := m.cfg
	database := m.db
	ctx := m.ctx
	return func() tea.Msg {
		files, err := scanner.Scan(cfg.Src, cfg.Ext)
		if err != nil {
			return scanDoneMsg{err: fmt.Errorf("scan %s: %w", cfg.Src, err)}
		}
		if len(files) == 0 {
			return scanDoneMsg{err: fmt.Errorf("no files matching %v under %s", cfg.Ext, cfg.Src)}
		}
		todo, skipped, blocked := worker.Plan(ctx, files, cfg, database)
		if len(todo) == 0 {
			return scanDoneMsg{err: fmt.Errorf("nothing to transcode — all outputs already exist (use --force to re-encode)")}
		}
		return scanDoneMsg{files: todo, skipped: skipped, blocked: blocked}
	}
}

// resolveCmd probes the hardware encoder off the UI goroutine, for the current
// codec/hwaccel. Repeated probes are deduped by ffmpeg's internal cache.
func (m Model) resolveCmd() tea.Cmd {
	codec, hw := m.cfg.Codec, m.cfg.HWAccel
	return func() tea.Msg {
		enc, be := ffmpeg.ResolveEncoder(m.ctx, codec, hw)
		return resolvedMsg{encoder: enc, backend: be, codec: codec, hwaccel: hw}
	}
}

// launch starts m.pool and feeds its events onto m.events. m.pool must be set
// (in handlePreflightKey) first, so p can later toggle its pause state.
func (m Model) launch() {
	pool := m.pool
	go func() {
		pool.RunWithCallbacks(m.ctx, m.files,
			func(ev ffmpeg.ProgressEvent) { m.events <- progressMsg{ev: ev} },
			func(f scanner.MediaFile, err error) { m.events <- completeMsg{file: f, err: err} },
		)
		m.events <- allDoneMsg{}
	}()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// List text sits inside a bordered+padded box; leave room for both
		// (2 border + 2 padding) so its rows never wrap against the frame.
		m.list.SetSize(m.panelWidth()-4, m.listHeight())
		return m, nil

	case scanDoneMsg:
		m.scanning = false
		if msg.err != nil {
			m.scanErr = msg.err
			return m, nil
		}
		m.initFileItems(msg.files)
		if msg.skipped > 0 {
			m.logs = append(m.logs, fmt.Sprintf("skipped %d already up-to-date file(s)", msg.skipped))
		}
		if msg.blocked > 0 {
			m.logs = append(m.logs, fmt.Sprintf("blocked %d Dolby Vision source(s); rerun with --i-know-this-drops-hdr to transcode them anyway", msg.blocked))
		}
		return m, nil

	case resolvedMsg:
		// Ignore a probe result for a setting the user has since changed.
		if msg.codec == m.cfg.Codec && msg.hwaccel == m.cfg.HWAccel {
			m.encoder, m.backend, m.resolved = msg.encoder, msg.backend, true
			m.cfg.Preset = reconcilePreset(m.backend, m.encoder, m.cfg.Preset)
		}
		return m, nil

	case progressMsg:
		idx, ok := m.fileIndex[msg.ev.FilePath]
		if !ok {
			return m, listenForEvent(m.events)
		}
		m.fileItems[idx].status = statusActive
		m.fileItems[idx].percent = msg.ev.Percent
		m.fileItems[idx].eta = msg.ev.ETA
		m.list.SetItem(idx, listEntry{
			name:   filepath.Base(msg.ev.FilePath),
			status: statusActive,
		})
		setCmd := m.fileItems[idx].prog.SetPercent(msg.ev.Percent)
		return m, tea.Batch(setCmd, listenForEvent(m.events))

	case completeMsg:
		return m.handleComplete(msg)

	case allDoneMsg:
		// While cancelling, allDone is the signal that every worker has stopped
		// and cleaned up its transient artifact — only now is it safe to exit.
		if m.cancelling {
			m.quitting = true
			return m, tea.Quit
		}
		// The pool finished on its own. It will never send another event, so we
		// must NOT wait on the channel again — a later q must quit directly.
		m.finished = true
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

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The help overlay toggles from any phase and swallows other keys.
	switch msg.String() {
	case "?":
		m.showHelp = !m.showHelp
		return m, nil
	case "esc":
		m.showHelp = false
		m.confirming = false
		return m, nil
	}
	if m.showHelp {
		return m, nil
	}
	if !m.started {
		return m.handlePreflightKey(msg)
	}
	return m.handleRunningKey(msg)
}

// handlePreflightKey runs before any job has started: navigate fields, adjust
// values, then start or abort. q/Ctrl+C always work; everything else is gated
// on the scan having completed successfully.
func (m Model) handlePreflightKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	}
	// Still scanning or scan failed — only q works.
	if m.scanning || m.scanErr != nil {
		return m, nil
	}
	// --inplace is destructive (replaces originals); mirror the CLI's y/N
	// prompt with a second explicit keypress instead of starting on enter.
	// --assume-yes (-y) skips this the same way it skips the CLI prompt.
	if m.confirming {
		switch msg.String() {
		case "y", "Y":
			m.confirming = false
			m.pool = worker.New(m.cfg, m.db)
			m.started = true
			m.launch()
			return m, listenForEvent(m.events)
		default:
			m.confirming = false
			return m, nil
		}
	}
	switch msg.String() {
	case "enter", "s":
		if m.cfg.InPlace && !m.cfg.AssumeYes {
			m.confirming = true
			return m, nil
		}
		m.pool = worker.New(m.cfg, m.db)
		m.started = true
		m.launch()
		return m, listenForEvent(m.events)
	case "down", "j", "tab":
		m.field = (m.field + 1) % confFieldCount
		return m, nil
	case "up", "k", "shift+tab":
		m.field = (m.field - 1 + confFieldCount) % confFieldCount
		return m, nil
	case "left", "h":
		return m.adjust(-1)
	case "right", "l":
		return m.adjust(+1)
	}
	return m, nil
}

// adjust changes the focused field by delta and, when the codec or hwaccel
// changed, re-probes so the resolved-encoder line stays accurate.
func (m Model) adjust(delta int) (tea.Model, tea.Cmd) {
	reResolve := false
	switch m.field {
	case fCodec:
		m.cfg.Codec = cycle(codecChoices, m.cfg.Codec, delta)
		reResolve = true
	case fCRF:
		m.cfg.CRF = clamp(m.cfg.CRF+delta, 0, 51)
	case fPreset:
		// Only the presets valid for the resolved encoder are offered.
		if choices := ffmpeg.PresetsFor(m.backend, m.encoder); len(choices) > 0 {
			m.cfg.Preset = cycle(choices, m.cfg.Preset, delta)
		}
	case fHWAccel:
		m.cfg.HWAccel = cycle(hwaccelChoices, m.cfg.HWAccel, delta)
		reResolve = true
	case fHWDecode:
		// No re-probe needed: decode probes are per-file at dispatch time and
		// keyed by backend, so the toggle only changes what the run will try.
		m.cfg.HWDecode = cycle(hwdecodeChoices, m.cfg.HWDecode, delta)
	case fWorkers:
		m.cfg.Workers = clamp(m.cfg.Workers+delta, 1, 256)
	case fDecodeThreads:
		m.cfg.DecodeThreads = clamp(m.cfg.DecodeThreads+delta, 0, 256)
	case fAudioCodec:
		m.cfg.AudioCodec = cycle(audioCodecChoices, m.cfg.AudioCodec, delta)
	case fAudioBitrate:
		// Ignored by ffmpeg.audioArgs when AudioCodec is copy; no-op here too so
		// the field visibly does nothing rather than silently queuing a value
		// that will never reach the ffmpeg invocation.
		if !strings.EqualFold(m.cfg.AudioCodec, "copy") && m.cfg.AudioCodec != "" {
			m.cfg.AudioBitrate = cycle(audioBitrateChoices, m.cfg.AudioBitrate, delta)
		}
	case fSubs:
		m.cfg.SubtitleMode = cycle(subsChoices, m.cfg.SubtitleMode, delta)
	case fInPlace:
		// --inplace is mutually exclusive with --output-dir/--to (config.Validate
		// enforces this at launch); those two are launch-only in this UI, so
		// rather than silently clearing a value the user passed on the CLI,
		// block turning --inplace on while either is set.
		want := !m.cfg.InPlace
		if want && (m.cfg.OutputDir != "" || m.cfg.Container != "") {
			return m, nil
		}
		m.cfg.InPlace = want
	}
	if reResolve {
		m.resolved, m.encoder, m.backend = false, "", ""
		return m, m.resolveCmd()
	}
	return m, nil
}

// cycle returns the choice delta steps from cur (wrapping). An unknown cur
// starts from the first choice.
func cycle(choices []string, cur string, delta int) string {
	idx := 0
	for i, c := range choices {
		if c == cur {
			idx = i
			break
		}
	}
	n := len(choices)
	return choices[((idx+delta)%n+n)%n]
}

// reconcilePreset keeps the preset valid for a (re-)resolved encoder: an unset
// encoder-preset namespace clears it; a now-invalid value snaps to the default.
func reconcilePreset(backend, encoder, cur string) string {
	choices := ffmpeg.PresetsFor(backend, encoder)
	if len(choices) == 0 {
		return "" // encoder takes no -preset
	}
	for _, c := range choices {
		if c == cur {
			return cur
		}
	}
	return ffmpeg.DefaultPreset(backend, encoder)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// handleRunningKey runs once the pool has started.
func (m Model) handleRunningKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		// Nothing left running — quit immediately. The pool already drained, so
		// there is no further event to wait for (waiting here would hang).
		if m.finished {
			m.quitting = true
			return m, tea.Quit
		}
		if m.cancelling {
			return m, nil // already draining; ignore repeat presses
		}
		// Graceful: cancel in-flight ffmpeg, then wait for the worker goroutine
		// to drain (allDoneMsg) so partial artifacts are cleaned up before exit.
		m.cancelling = true
		m.cancel()
		m.logs = append(m.logs, "cancelling — waiting for active jobs to stop…")
		return m, listenForEvent(m.events)

	case "Q":
		// Force-quit: cancel and exit immediately without waiting to drain.
		m.cancel()
		m.quitting = true
		return m, tea.Quit

	case "p":
		// Pause/resume starting new jobs; in-flight ffmpeg is untouched.
		if m.pool != nil && !m.finished && !m.cancelling {
			m.paused = m.pool.TogglePause()
		}
		return m, nil
	}

	// Everything else (↑/↓, j/k, g/G, …) drives the file-queue list.
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m Model) handleComplete(msg completeMsg) (tea.Model, tea.Cmd) {
	idx, ok := m.fileIndex[msg.file.Path]
	if !ok {
		return m, listenForEvent(m.events)
	}
	name := filepath.Base(msg.file.Path)

	switch {
	case msg.err == nil:
		m.fileItems[idx].status = statusDone
		m.fileItems[idx].percent = 1
		_ = m.fileItems[idx].prog.SetPercent(1)
		m.done++
		m.logs = append(m.logs, fmt.Sprintf("✓ %s", name))
		m.list.SetItem(idx, listEntry{name: name, status: statusDone})

	case m.cancelling:
		// A failure that arrives after the user asked to quit is a cancellation,
		// not a real error — don't paint it red or inflate the error count.
		m.fileItems[idx].status = statusCancelled
		m.list.SetItem(idx, listEntry{name: name, status: statusCancelled})

	default:
		m.fileItems[idx].status = statusError
		m.errCount++
		m.logs = append(m.logs, fmt.Sprintf("✗ %s: %v", name, msg.err))
		m.list.SetItem(idx, listEntry{name: name, status: statusError})
	}
	return m, listenForEvent(m.events)
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.width == 0 {
		return "initializing…\n"
	}
	if m.showHelp {
		return m.helpView()
	}
	if m.scanning {
		return m.scanningView()
	}
	if m.scanErr != nil {
		return m.scanErrView()
	}
	if !m.started {
		if m.confirming {
			return m.confirmInplaceView()
		}
		return m.preflightView()
	}

	statsStr := fmt.Sprintf("files: %d/%d  errors: %d  workers: %d",
		m.done, len(m.files), m.errCount, m.cfg.Workers)

	header := lipgloss.JoinHorizontal(lipgloss.Top,
		theme.Title.Render("⚡ smelt"),
		"  ",
		theme.Subtitle.Render(statsStr),
	)
	if m.paused {
		header = lipgloss.JoinHorizontal(lipgloss.Top, header, "  ", theme.StatusAct.Render("⏸ paused"))
	}

	// Force each panel to span the terminal; without an explicit width lipgloss
	// shrinks a bordered box to its longest line, hugging the left edge.
	cw := m.panelWidth() - 4 // content width inside border (2) + padding (2)
	box := theme.Box.Width(cw)
	logBox := theme.LogBox.Width(cw)

	sections := []string{header, "", box.Render(m.list.View()), ""}

	if active := m.renderActiveFiles(); active != "" {
		sections = append(sections, box.Render(active), "")
	}

	sections = append(sections, logBox.Render(m.renderLogs()))
	sections = append(sections, m.statusBar())

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m Model) statusBar() string {
	if m.cancelling {
		return theme.StatusCxl.Render("cancelling… (Q force-quit)")
	}
	if m.paused {
		return theme.StatusAct.Render("paused — p resume · q quit")
	}
	return theme.Help.Render("q quit · Q force-quit · p pause · ↑↓/jk navigate · ? help")
}

func (m Model) renderActiveFiles() string {
	var lines []string
	overflow := 0
	for _, fi := range m.fileItems {
		if fi.status != statusActive {
			continue
		}
		if len(lines) >= maxActiveRows {
			overflow++
			continue
		}
		lines = append(lines, renderActiveFile(fi))
	}
	if overflow > 0 {
		lines = append(lines, theme.StatusPend.Render(fmt.Sprintf("  …and %d more", overflow)))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderLogs() string {
	logs := m.logs
	if len(logs) > maxLogLines {
		logs = logs[len(logs)-maxLogLines:]
	}
	if len(logs) == 0 {
		return "waiting for workers…"
	}
	return strings.Join(logs, "\n")
}

// panelWidth is the on-screen width of each panel: the terminal width minus a
// one-column breathing margin on each side, floored so it never goes negative.
func (m Model) panelWidth() int {
	if m.width < 24 {
		return 20
	}
	return m.width - 2
}

// listHeight budgets the file-queue panel: total terminal height minus the
// fixed-size header, active-worker, log, and status-bar panels. The queue gets
// the remainder, floored so it never collapses.
func (m Model) listHeight() int {
	const (
		headerRows  = 2 // logo line + blank
		statusRows  = 1
		listBorders = 2 // rounded border top+bottom around the list
	)
	activeRows := maxActiveRows + 2 // worst-case active panel (rows + border)
	logRows := maxLogLines + 2
	h := m.height - headerRows - statusRows - listBorders - activeRows - logRows
	if h < 3 {
		h = 3
	}
	return h
}

func (m Model) scanningView() string {
	content := theme.Title.Render("⚡ smelt") + "\n\n" +
		theme.Subtitle.Render("scanning "+m.cfg.Src+"…") + "\n\n" +
		theme.Help.Render("q to abort")
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, theme.Box.Render(content))
}

func (m Model) scanErrView() string {
	content := theme.Title.Render("⚡ smelt — error") + "\n\n" +
		theme.StatusErr.Render(m.scanErr.Error()) + "\n\n" +
		theme.Help.Render("q to quit")
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, theme.Box.Render(content))
}

// preflightView is the editable pre-start screen: static context (src/output)
// plus the adjustable encode settings, then start/abort.
func (m Model) preflightView() string {
	files := "files"
	if len(m.files) == 1 {
		files = "file"
	}

	// static context rows
	static := func(k, v string) string {
		return theme.StatusPend.Render(fmt.Sprintf("  %-9s", k)) + theme.FileLabel.Render(v)
	}
	// an editable row: ‹ value › with the focused field highlighted.
	edit := func(f confField, k, v, suffix string) string {
		focused := f == m.field
		marker := "  "
		ctrl := fmt.Sprintf("‹ %s ›", v)
		if focused {
			marker = theme.StatusAct.Render("▸ ")
			ctrl = theme.StatusAct.Render(ctrl)
		} else {
			ctrl = theme.FileLabel.Render(ctrl)
		}
		return marker + theme.Subtitle.Render(fmt.Sprintf("%-9s", k)) + ctrl + theme.Subtitle.Render(suffix)
	}

	preset := m.cfg.Preset
	if m.resolved && len(ffmpeg.PresetsFor(m.backend, m.encoder)) == 0 {
		preset = "n/a"
	} else if preset == "" {
		preset = "—"
	}

	decodeThreads := "uncapped"
	if m.cfg.DecodeThreads > 0 {
		decodeThreads = strconv.Itoa(m.cfg.DecodeThreads)
	}

	audioBitrate := m.cfg.AudioBitrate
	switch {
	case strings.EqualFold(m.cfg.AudioCodec, "copy") || m.cfg.AudioCodec == "":
		audioBitrate = "n/a"
	case audioBitrate == "":
		audioBitrate = "—"
	}

	inplace := "off"
	inplaceSuffix := ""
	if m.cfg.InPlace {
		inplace = "on"
		inplaceSuffix = "  ⚠ replaces originals"
	}

	var b strings.Builder
	b.WriteString(theme.Title.Render("⚡ smelt — configure"))
	b.WriteString("\n\n")
	b.WriteString(static("src", fmt.Sprintf("%s  (%d %s)", m.cfg.Src, len(m.files), files)) + "\n")
	b.WriteString(static("output", m.outputSummary()) + "\n\n")
	b.WriteString(edit(fCodec, "codec", m.cfg.Codec, "") + "\n")
	b.WriteString(edit(fCRF, "crf", strconv.Itoa(m.cfg.CRF), "") + "\n")
	b.WriteString(edit(fPreset, "preset", preset, "") + "\n")
	b.WriteString(edit(fHWAccel, "hwaccel", m.cfg.HWAccel, "  → "+m.resolvedEncoder()) + "\n")
	b.WriteString(edit(fHWDecode, "hwdecode", m.cfg.HWDecode, "") + "\n")
	b.WriteString(edit(fWorkers, "workers", strconv.Itoa(m.cfg.Workers), "") + "\n")
	b.WriteString(edit(fDecodeThreads, "decode", decodeThreads, "") + "\n")
	if line := m.resourceProfileLine(); line != "" {
		b.WriteString(line + "\n")
	}
	b.WriteString(edit(fAudioCodec, "audio", m.cfg.AudioCodec, "") + "\n")
	b.WriteString(edit(fAudioBitrate, "bitrate", audioBitrate, "") + "\n")
	b.WriteString(edit(fSubs, "subs", m.cfg.SubtitleMode, "") + "\n")
	b.WriteString(edit(fInPlace, "inplace", inplace, inplaceSuffix) + "\n")
	b.WriteString("\n")
	b.WriteString(theme.Help.Render("  [↑↓/tab] field   [←→] change   [enter] start   [q] abort   [?] help"))
	// Center the card so it scales with the terminal instead of hugging the corner.
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, theme.Box.Render(b.String()))
}

// confirmInplaceView blocks the pre-flight screen with a y/N-style prompt
// before a destructive --inplace run, matching the CLI's confirmInplace.
func (m Model) confirmInplaceView() string {
	files := "files"
	if len(m.files) == 1 {
		files = "file"
	}
	content := theme.Title.Render("⚡ smelt — confirm") + "\n\n" +
		theme.StatusErr.Render(fmt.Sprintf("--inplace will permanently replace %d original %s.", len(m.files), files)) + "\n\n" +
		theme.Help.Render("  [y] continue   [any other key] cancel")
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, theme.Box.Render(content))
}

// resolvedEncoder is the concrete encoder for the hwaccel row, or a placeholder
// while the probe is in flight.
func (m Model) resolvedEncoder() string {
	if !m.resolved {
		return "resolving…"
	}
	if m.backend == "" {
		return m.encoder + " (software)"
	}
	return m.encoder
}

// resourceProfileLine mirrors the CLI's LogResourceProfile warning on-screen:
// smelt only ever accelerates encode, so a hardware backend means uncapped
// software decode runs concurrently with the GPU/QSV/NVENC encode block —
// the same information a TUI run would otherwise only get via a log line it
// never renders. Empty while the probe is still in flight.
func (m Model) resourceProfileLine() string {
	if !m.resolved {
		return ""
	}
	p := worker.BuildResourceProfile(m.encoder, m.backend, m.cfg.DecodeThreads, m.cfg.HWDecode)
	label := fmt.Sprintf("  decode: %s", p.DecodeLabel())
	if p.DecodeHW {
		return theme.StatusDone.Render(label)
	}
	if !p.Warn {
		return theme.StatusPend.Render(label)
	}
	return theme.StatusErr.Render(label + "  ⚠ concurrent with hardware encode — see --hwdecode/--decode-threads/--workers")
}

// outputSummary describes where finished files will land.
func (m Model) outputSummary() string {
	switch {
	case m.cfg.InPlace:
		return theme.StatusErr.Render("in place — replaces originals")
	case m.cfg.OutputDir != "":
		return m.cfg.OutputDir + "  (mirrored tree)"
	case m.cfg.Container != "":
		return "*" + m.cfg.Suffix + "." + m.cfg.Container + "  (alongside source)"
	default:
		return "*" + m.cfg.Suffix + ".<ext>  (alongside source, same container)"
	}
}

func (m Model) helpView() string {
	rows := [][2]string{
		{"enter / s", "start the run (pre-flight screen only)"},
		{"p", "pause / resume starting new jobs (running jobs continue)"},
		{"q / Ctrl+C", "cancel active jobs, wait for cleanup, then quit"},
		{"Q", "force-quit immediately (cancels jobs, skips waiting)"},
		{"↑ / k", "move selection up in the file queue"},
		{"↓ / j", "move selection down in the file queue"},
		{"?", "toggle this help"},
		{"esc", "close this help"},
	}
	var b strings.Builder
	b.WriteString(theme.Title.Render("⚡ smelt — keybindings"))
	b.WriteString("\n\n")
	for _, r := range rows {
		b.WriteString(theme.StatusAct.Render(fmt.Sprintf("  %-12s", r[0])))
		b.WriteString(theme.FileLabel.Render(r[1]))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(theme.Help.Render("press ? or esc to return"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, theme.Box.Render(b.String()))
}

// listenForEvent returns a Cmd that blocks until the next event arrives on ch.
func listenForEvent(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}
