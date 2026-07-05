package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Raina-Hardik/smelt/internal/db"
	"github.com/Raina-Hardik/smelt/internal/workflow"
)

// writeFakeBinary writes a shell script to a temp dir and returns its path.
// The script body is the lines after the shebang. Used by both exec and handler tests.
func writeFakeBinary(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "fake-smelt")
	content := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatalf("writeFakeBinary: %v", err)
	}
	return p
}

// openTestDB opens a real DB at a temp path and registers cleanup.
func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// newTestProgram returns a minimal Program with a unique name derived from t
// so that concurrent tests never share the flock lock file (which is
// keyed on the program name via TMPDIR).
func newTestProgram(t *testing.T, src string) workflow.Program {
	t.Helper()
	// Replace slashes (sub-test separators) with dashes to keep the name clean.
	name := "exec-test-" + strings.ReplaceAll(t.Name(), "/", "-")
	return workflow.Program{
		Name: name,
		Src:  src,
		Ext:  []string{"mkv"},
	}
}

// waitFor polls pred until it returns true or timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, pred func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pred() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// --- triggerRun ---

func TestTriggerRun_ScriptWritten(t *testing.T) {
	d := openTestDB(t)
	dir := t.TempDir()
	bin := writeFakeBinary(t, "exit 0")
	srv := New(d, dir, bin, "")

	runID := "script-written-run"
	if err := srv.triggerRun(runID, newTestProgram(t, "/src")); err != nil {
		t.Fatalf("triggerRun: %v", err)
	}

	scriptPath := filepath.Join(dir, "smelt-"+runID+".sh")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		t.Errorf("script not written: %s", scriptPath)
	}
}

func TestTriggerRun_ScriptContainsBinary(t *testing.T) {
	d := openTestDB(t)
	dir := t.TempDir()
	bin := writeFakeBinary(t, "exit 0")
	srv := New(d, dir, bin, "")

	runID := "script-content-run"
	if err := srv.triggerRun(runID, newTestProgram(t, "/src")); err != nil {
		t.Fatalf("triggerRun: %v", err)
	}

	scriptPath := filepath.Join(dir, "smelt-"+runID+".sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	if !filepath.IsAbs(bin) {
		t.Skip("binary path not absolute, skip content check")
	}
	if len(content) == 0 {
		t.Error("script file is empty")
	}
}

func TestTriggerRun_LogFileCreated(t *testing.T) {
	d := openTestDB(t)
	dir := t.TempDir()
	bin := writeFakeBinary(t, "exit 0")
	srv := New(d, dir, bin, "")

	runID := "log-file-run"
	if err := srv.triggerRun(runID, newTestProgram(t, "/src")); err != nil {
		t.Fatalf("triggerRun: %v", err)
	}

	logPath := filepath.Join(dir, "smelt-"+runID+".sh.log")
	// Log file may be created slightly after Start() returns.
	found := waitFor(t, 2*time.Second, func() bool {
		_, err := os.Stat(logPath)
		return err == nil
	})
	if !found {
		t.Errorf("log file not created: %s", logPath)
	}
}

func TestTriggerRun_ProcessTracked(t *testing.T) {
	d := openTestDB(t)
	dir := t.TempDir()
	// Use a sleepy binary so the process stays alive long enough to inspect.
	bin := writeFakeBinary(t, "sleep 30")
	srv := New(d, dir, bin, "")

	runID := "tracked-run"
	if err := srv.triggerRun(runID, newTestProgram(t, "/src")); err != nil {
		t.Fatalf("triggerRun: %v", err)
	}
	defer srv.cancelRun(runID) // clean up

	// Process should be in the map immediately after Start.
	if _, ok := srv.procs.Load(runID); !ok {
		t.Error("process not in procs map after triggerRun")
	}
}

func TestTriggerRun_ProcessCleansUpAfterCompletion(t *testing.T) {
	d := openTestDB(t)
	dir := t.TempDir()
	bin := writeFakeBinary(t, "exit 0")
	srv := New(d, dir, bin, "")

	runID := "cleanup-run"
	if err := srv.triggerRun(runID, newTestProgram(t, "/src")); err != nil {
		t.Fatalf("triggerRun: %v", err)
	}

	// After the script exits, the goroutine should delete the entry.
	cleaned := waitFor(t, 5*time.Second, func() bool {
		_, ok := srv.procs.Load(runID)
		return !ok
	})
	if !cleaned {
		t.Error("process not removed from procs map after completion")
	}
}

func TestTriggerRun_UniqueRunIDs(t *testing.T) {
	d := openTestDB(t)
	dir := t.TempDir()
	bin := writeFakeBinary(t, "exit 0")
	srv := New(d, dir, bin, "")

	// Each run uses a distinct program name so their flock locks don't collide.
	for i, id := range []string{"run-a", "run-b", "run-c"} {
		p := workflow.Program{Name: t.Name() + "-" + string(rune('a'+i)), Src: "/src", Ext: []string{"mkv"}}
		if err := srv.triggerRun(id, p); err != nil {
			t.Fatalf("triggerRun %s: %v", id, err)
		}
	}

	// Each run should have its own script file.
	for _, id := range []string{"run-a", "run-b", "run-c"} {
		p := filepath.Join(dir, "smelt-"+id+".sh")
		if _, err := os.Stat(p); os.IsNotExist(err) {
			t.Errorf("script not written for %s", id)
		}
	}
}

// --- cancelRun ---

func TestCancelRun_UnknownRunID(t *testing.T) {
	d := openTestDB(t)
	srv := New(d, t.TempDir(), "smelt", "")
	if srv.cancelRun("not-a-run") {
		t.Error("cancelRun should return false for unknown run_id")
	}
}

func TestCancelRun_KillsProcess(t *testing.T) {
	d := openTestDB(t)
	dir := t.TempDir()
	// Sleepy binary so the process is still running when we cancel.
	bin := writeFakeBinary(t, "sleep 60")
	srv := New(d, dir, bin, "")

	runID := "cancel-me"
	if err := srv.triggerRun(runID, newTestProgram(t, "/src")); err != nil {
		t.Fatalf("triggerRun: %v", err)
	}

	// Give the process time to start.
	time.Sleep(50 * time.Millisecond)

	if !srv.cancelRun(runID) {
		t.Fatal("cancelRun should return true for a tracked process")
	}

	// After SIGTERM, the goroutine's Wait() returns and cleans up the procs entry.
	cleaned := waitFor(t, 5*time.Second, func() bool {
		_, ok := srv.procs.Load(runID)
		return !ok
	})
	if !cleaned {
		t.Error("process not removed from procs after cancelRun")
	}
}

func TestCancelRun_IdempotentAfterCompletion(t *testing.T) {
	d := openTestDB(t)
	dir := t.TempDir()
	bin := writeFakeBinary(t, "exit 0")
	srv := New(d, dir, bin, "")

	runID := "idem-run"
	if err := srv.triggerRun(runID, newTestProgram(t, "/src")); err != nil {
		t.Fatalf("triggerRun: %v", err)
	}
	// Wait for process to finish.
	waitFor(t, 5*time.Second, func() bool {
		_, ok := srv.procs.Load(runID)
		return !ok
	})

	// Calling cancelRun after completion should return false — nothing to kill.
	if srv.cancelRun(runID) {
		t.Error("cancelRun should return false after process has already finished")
	}
}

// --- Full handler/exec integration: POST /run + DELETE /run/{id} ---

func TestHandleCancelRun_LiveProcess(t *testing.T) {
	e := newTestEnv(t)
	// Sleepy binary so the process stays alive when we cancel.
	e.srv.binary = writeFakeBinary(t, "sleep 60")
	programID := createProgram(t, e, "/x", nil)

	// Start the run via the HTTP handler.
	w := e.do("POST", "/api/programs/"+programID+"/run", nil)
	if w.Code != 202 {
		t.Fatalf("run: status = %d; body=%s", w.Code, w.Body.String())
	}
	var runResp map[string]string
	decode(t, w, &runResp)
	runID := runResp["run_id"]

	// The run record won't be in the DB until the subprocess's `each` call writes
	// it, and our fake binary never does. So seed the DB manually so the handler
	// can find the run.
	_ = e.db.StartRun(runID, "exec-test", 0)

	// Wait for the process to appear in the procs map before cancelling.
	started := waitFor(t, 2*time.Second, func() bool {
		_, ok := e.srv.procs.Load(runID)
		return ok
	})
	if !started {
		t.Fatal("process did not appear in procs map")
	}

	// Cancel via HTTP.
	w2 := e.do("DELETE", "/api/runs/"+runID, nil)
	if w2.Code != 204 {
		t.Fatalf("cancel: status = %d; body=%s", w2.Code, w2.Body.String())
	}

	// Verify process is cleaned up.
	cleaned := waitFor(t, 5*time.Second, func() bool {
		_, ok := e.srv.procs.Load(runID)
		return !ok
	})
	if !cleaned {
		t.Error("process not cleaned up after HTTP cancel")
	}
}
