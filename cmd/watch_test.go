package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/worker"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// resetWatchFlags restores watchCmd's flags to their defaults. cobra reuses
// the package-level command across Execute() calls, so a flag set to a
// non-default value by one test (e.g. --dry-run) would otherwise leak into
// the next test that doesn't explicitly pass it.
func resetWatchFlags(t *testing.T) {
	t.Helper()
	watchCmd.Flags().VisitAll(func(f *pflag.Flag) {
		// pflag's slice/array Value.Set(DefValue) re-parses the bracketed
		// String() form as CSV (e.g. "[mkv,mp4,avi]" -> "[mkv", "avi]"),
		// corrupting it. None of the watch tests touch these flags, so
		// leave them alone rather than "resetting" them into garbage.
		switch f.Value.Type() {
		case "stringSlice", "stringArray", "intSlice":
			return
		}
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
}

func TestWatchRequiresPositiveInterval(t *testing.T) {
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); resetWatchFlags(t) })

	rootCmd.SetArgs([]string{"watch", "--src", t.TempDir(), "--interval", "0s", "--db", ""})
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)

	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--interval must be > 0") {
		t.Fatalf("expected interval validation error, got %v", err)
	}
}

func TestWatchOnceDryRunNoFiles(t *testing.T) {
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); resetWatchFlags(t) })

	rootCmd.SetArgs([]string{"watch", "--src", t.TempDir(), "--once", "--dry-run", "--db", ""})
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("watch --once --dry-run on empty dir: %v", err)
	}
}

func TestWatchOnceReportsFailureExitCode(t *testing.T) {
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); resetWatchFlags(t) })

	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.mp4")
	if err := os.WriteFile(bad, []byte("not a real media file"), 0o644); err != nil {
		t.Fatal(err)
	}

	rootCmd.SetArgs([]string{"watch", "--src", dir, "--once", "--db", "", "--workers", "1"})
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)

	err := rootCmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected an *ExitError, got %v (%T)", err, err)
	}
	if exitErr.Code != 3 {
		t.Errorf("exit code = %d, want 3", exitErr.Code)
	}
}

// TestWatchPassSkipsAlreadyTranscoded exercises the core watch behavior: a
// second pass over an unchanged source directory must leave the already
// -produced output alone (worker.Plan's smart-skip), not re-transcode it.
func TestWatchPassSkipsAlreadyTranscoded(t *testing.T) {
	src, err := filepath.Abs(filepath.Join("..", "testdata", "sample1.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Skipf("testdata sample not available: %v", err)
	}

	dir := t.TempDir()
	dst := filepath.Join(dir, "sample1.mp4")
	copyFile(t, src, dst)

	cfg := config.Defaults()
	cfg.Src = dir
	cfg.Ext = []string{"mp4"}
	cfg.Workers = 1
	cfg.Codec = "h264"

	pool := worker.New(cfg, nil)
	ctx := context.Background()

	if err := watchPass(ctx, cfg, pool, nil, "test", false); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	out := filepath.Join(dir, "sample1.smelt.mp4")
	info1, err := os.Stat(out)
	if err != nil {
		t.Fatalf("expected output %s to exist after first pass: %v", out, err)
	}

	if err := watchPass(ctx, cfg, pool, nil, "test", false); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	info2, err := os.Stat(out)
	if err != nil {
		t.Fatalf("output disappeared after second pass: %v", err)
	}
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Errorf("second pass re-transcoded an already-up-to-date file (mtime changed)")
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
}
