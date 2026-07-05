//go:build integration

package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/worker"
	"github.com/spf13/viper"
)

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
