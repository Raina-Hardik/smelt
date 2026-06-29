package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/scanner"
	"github.com/Raina-Hardik/smelt/internal/worker"
	tea "github.com/charmbracelet/bubbletea"
)

// keyMsg builds a tea.KeyMsg whose String() matches s (e.g. "q", "Q", "?", "esc").
func keyMsg(s string) tea.KeyMsg {
	if s == "esc" {
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func newTestModel(t *testing.T, paths ...string) Model {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // tear down the pool goroutine if a test starts it
	files := make([]scanner.MediaFile, len(paths))
	for i, p := range paths {
		files[i] = scanner.MediaFile{Path: p}
	}
	return New(&config.Config{Workers: 2, Codec: "h264", HWAccel: "auto", Suffix: ".smelt"}, files, ctx, nil)
}

// running returns a model already past the pre-flight screen.
func running(m Model) Model {
	m.started = true
	return m
}

func TestLabel(t *testing.T) {
	cases := map[fileStatus]string{
		statusPending:   "pending",
		statusActive:    "transcoding",
		statusDone:      "done",
		statusError:     "error",
		statusCancelled: "cancelled",
	}
	for s, want := range cases {
		if got := s.label(); got != want {
			t.Errorf("status %d label = %q, want %q", s, got, want)
		}
	}
}

func TestRenderLogsCapsTail(t *testing.T) {
	m := newTestModel(t, "/x/a.mkv")
	for i := 0; i < maxLogLines+5; i++ {
		m.logs = append(m.logs, string(rune('a'+i)))
	}
	out := m.renderLogs()
	if got := strings.Count(out, "\n") + 1; got != maxLogLines {
		t.Errorf("renderLogs kept %d lines, want %d", got, maxLogLines)
	}
	// The most recent line must survive; the oldest must be dropped.
	if !strings.Contains(out, string(rune('a'+maxLogLines+4))) {
		t.Error("renderLogs dropped the newest log line")
	}
	if strings.Contains(out, "\na\n") || strings.HasPrefix(out, "a\n") {
		t.Error("renderLogs kept an entry older than the tail window")
	}
}

func TestHandleCompleteSuccess(t *testing.T) {
	m := newTestModel(t, "/x/a.mkv")
	out, _ := m.handleComplete(completeMsg{file: scanner.MediaFile{Path: "/x/a.mkv"}, err: nil})
	m = out.(Model)
	if m.done != 1 || m.errCount != 0 {
		t.Fatalf("success: done=%d err=%d, want 1/0", m.done, m.errCount)
	}
	if m.fileItems[0].status != statusDone {
		t.Errorf("status = %v, want statusDone", m.fileItems[0].status)
	}
}

func TestHandleCompleteError(t *testing.T) {
	m := newTestModel(t, "/x/a.mkv")
	out, _ := m.handleComplete(completeMsg{file: scanner.MediaFile{Path: "/x/a.mkv"}, err: context.DeadlineExceeded})
	m = out.(Model)
	if m.errCount != 1 || m.fileItems[0].status != statusError {
		t.Fatalf("error: errCount=%d status=%v, want 1/statusError", m.errCount, m.fileItems[0].status)
	}
}

// A failure arriving after the user asked to quit is a cancellation, not an
// error: it must not paint the file red or inflate the error count.
func TestHandleCompleteWhileCancelling(t *testing.T) {
	m := newTestModel(t, "/x/a.mkv")
	m.cancelling = true
	out, _ := m.handleComplete(completeMsg{file: scanner.MediaFile{Path: "/x/a.mkv"}, err: context.Canceled})
	m = out.(Model)
	if m.errCount != 0 {
		t.Errorf("errCount = %d while cancelling, want 0", m.errCount)
	}
	if m.fileItems[0].status != statusCancelled {
		t.Errorf("status = %v, want statusCancelled", m.fileItems[0].status)
	}
}

func TestQuitKeyCancelsContextAndWaits(t *testing.T) {
	m := running(newTestModel(t, "/x/a.mkv"))
	out, cmd := m.handleKey(keyMsg("q"))
	m = out.(Model)
	if !m.cancelling {
		t.Error("q should set cancelling")
	}
	if m.ctx.Err() == nil {
		t.Error("q should cancel the worker context")
	}
	if m.quitting {
		t.Error("q must not quit immediately; it waits for allDoneMsg")
	}
	if cmd == nil {
		t.Error("q should keep listening for the drain to finish")
	}
}

func TestForceQuitKeyExitsImmediately(t *testing.T) {
	m := running(newTestModel(t, "/x/a.mkv"))
	out, _ := m.handleKey(keyMsg("Q"))
	m = out.(Model)
	if !m.quitting || m.ctx.Err() == nil {
		t.Errorf("Q should quit immediately and cancel ctx; quitting=%v err=%v", m.quitting, m.ctx.Err())
	}
}

func TestAllDoneWhileCancellingQuits(t *testing.T) {
	m := newTestModel(t, "/x/a.mkv")
	m.cancelling = true
	out, _ := m.Update(allDoneMsg{})
	if !out.(Model).quitting {
		t.Error("allDoneMsg while cancelling should quit")
	}
}

// Regression: pressing q after the pool already drained must quit immediately,
// not wait on the (now silent) event channel forever.
func TestQuitAfterFinishedExitsImmediately(t *testing.T) {
	m := running(newTestModel(t, "/x/a.mkv"))
	out, _ := m.Update(allDoneMsg{})
	m = out.(Model)
	if !m.finished {
		t.Fatal("allDoneMsg should mark the run finished")
	}
	out, cmd := m.handleKey(keyMsg("q"))
	m = out.(Model)
	if !m.quitting || m.cancelling {
		t.Errorf("q after finish: quitting=%v cancelling=%v, want true/false", m.quitting, m.cancelling)
	}
	if cmd == nil {
		t.Error("q after finish should return tea.Quit, not nil")
	}
}

func TestPauseTogglesPoolAndState(t *testing.T) {
	m := running(newTestModel(t, "/x/a.mkv"))
	m.pool = worker.New(m.cfg, nil)

	out, _ := m.handleKey(keyMsg("p"))
	m = out.(Model)
	if !m.paused || !m.pool.Paused() {
		t.Fatalf("p should pause: model=%v pool=%v", m.paused, m.pool.Paused())
	}
	out, _ = m.handleKey(keyMsg("p"))
	m = out.(Model)
	if m.paused || m.pool.Paused() {
		t.Errorf("second p should resume: model=%v pool=%v", m.paused, m.pool.Paused())
	}
}

func TestPreflightQuitAbortsImmediately(t *testing.T) {
	m := newTestModel(t, "/x/a.mkv") // not started
	out, _ := m.handleKey(keyMsg("q"))
	m = out.(Model)
	if !m.quitting || m.started || m.cancelling {
		t.Errorf("preflight q: quitting=%v started=%v cancelling=%v, want true/false/false",
			m.quitting, m.started, m.cancelling)
	}
}

func TestPreflightEnterStartsPool(t *testing.T) {
	m := newTestModel(t, "/x/a.mkv")
	out, cmd := m.handleKey(keyMsg("enter"))
	m = out.(Model)
	if !m.started {
		t.Error("enter should start the run")
	}
	if cmd == nil {
		t.Error("starting should subscribe to the event channel")
	}
}

func TestResolvedMsgPopulatesEncoder(t *testing.T) {
	m := newTestModel(t, "/x/a.mkv") // cfg: codec h264, hwaccel auto
	out, _ := m.Update(resolvedMsg{encoder: "h264_nvenc", backend: "nvenc", codec: "h264", hwaccel: "auto"})
	m = out.(Model)
	if !m.resolved || m.encoder != "h264_nvenc" {
		t.Fatalf("resolved=%v encoder=%q, want true/h264_nvenc", m.resolved, m.encoder)
	}
	if got := m.resolvedEncoder(); got != "h264_nvenc" {
		t.Errorf("resolvedEncoder = %q, want h264_nvenc", got)
	}
}

// A probe result for a setting the user already changed away from is ignored.
func TestStaleResolvedMsgIgnored(t *testing.T) {
	m := newTestModel(t, "/x/a.mkv") // hwaccel auto
	out, _ := m.Update(resolvedMsg{encoder: "h264_qsv", backend: "qsv", codec: "h264", hwaccel: "qsv"})
	if out.(Model).resolved {
		t.Error("a resolvedMsg for hwaccel=qsv must be ignored when cfg.HWAccel=auto")
	}
}

func TestAdjustCodecReResolves(t *testing.T) {
	m := newTestModel(t, "/x/a.mkv")
	m.field = fCodec
	out, cmd := m.adjust(+1)
	m = out.(Model)
	if m.cfg.Codec != "h265" { // h264 → next in codecChoices
		t.Errorf("codec after +1 = %q, want h265", m.cfg.Codec)
	}
	if m.resolved || cmd == nil {
		t.Error("changing codec should clear resolved and trigger a re-probe cmd")
	}
}

func TestReconcilePresetSnapsToValid(t *testing.T) {
	cases := []struct{ backend, encoder, cur, want string }{
		{"nvenc", "hevc_nvenc", "medium", "p5"}, // x264 name invalid for nvenc → default
		{"nvenc", "hevc_nvenc", "p3", "p3"},     // already valid → kept
		{"", "libx265", "slow", "slow"},         // valid x264 preset kept
		{"", "libvpx-vp9", "medium", ""},        // vp9 takes no preset → cleared
		{"vaapi", "hevc_vaapi", "medium", ""},   // vaapi takes no preset → cleared
		{"", "libsvtav1", "veryfast", "8"},      // svt name not in numeric menu → default
	}
	for _, c := range cases {
		if got := reconcilePreset(c.backend, c.encoder, c.cur); got != c.want {
			t.Errorf("reconcilePreset(%q,%q,%q) = %q, want %q", c.backend, c.encoder, c.cur, got, c.want)
		}
	}
}

// Resolving onto a HW encoder must snap an x264 preset to one that encoder
// actually accepts, so the run can't fail with the preset the user sees.
func TestResolvedMsgReconcilesPreset(t *testing.T) {
	m := newTestModel(t, "/x/a.mkv")
	m.cfg.Preset = "superfast" // x264 name, invalid for nvenc
	out, _ := m.Update(resolvedMsg{encoder: "h264_nvenc", backend: "nvenc", codec: "h264", hwaccel: "auto"})
	if got := out.(Model).cfg.Preset; got != "p5" {
		t.Errorf("preset after resolving to nvenc = %q, want p5", got)
	}
}

func TestAdjustPresetCyclesResolvedSet(t *testing.T) {
	m := newTestModel(t, "/x/a.mkv")
	m.backend, m.encoder, m.resolved = "nvenc", "hevc_nvenc", true
	m.cfg.Preset, m.field = "p5", fPreset
	if out, _ := m.adjust(+1); out.(Model).cfg.Preset != "p6" {
		t.Errorf("nvenc preset +1 from p5 = %q, want p6", out.(Model).cfg.Preset)
	}
}

func TestAdjustCRFClamps(t *testing.T) {
	m := newTestModel(t, "/x/a.mkv")
	m.field = fCRF
	m.cfg.CRF = 0
	if out, _ := m.adjust(-1); out.(Model).cfg.CRF != 0 {
		t.Errorf("crf clamped low = %d, want 0", out.(Model).cfg.CRF)
	}
}

func TestPreflightFieldNavWraps(t *testing.T) {
	m := newTestModel(t, "/x/a.mkv")
	out, _ := m.handleKey(keyMsg("up")) // from fCodec(0) up wraps to last
	if out.(Model).field != confFieldCount-1 {
		t.Errorf("field after up from first = %d, want %d", out.(Model).field, confFieldCount-1)
	}
}

func TestListHeightHasFloor(t *testing.T) {
	m := newTestModel(t, "/x/a.mkv")
	m.height = 5 // tiny terminal
	if h := m.listHeight(); h < 3 {
		t.Errorf("listHeight = %d, want floor of 3", h)
	}
}

func TestHelpToggle(t *testing.T) {
	m := newTestModel(t, "/x/a.mkv")
	out, _ := m.handleKey(keyMsg("?"))
	if !out.(Model).showHelp {
		t.Fatal("? should open help")
	}
	out, _ = out.(Model).handleKey(keyMsg("esc"))
	if out.(Model).showHelp {
		t.Error("esc should close help")
	}
}
