package cmd

import (
	"bytes"
	"strings"
	"testing"

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

// TestWatchRequiresPositiveInterval is the one watch test that needs no
// ffmpeg on PATH: runWatch validates --interval before checking for ffmpeg,
// so this must run (and fail fast) even on a machine with no encoder
// installed. The ffmpeg-dependent watch tests live in
// watch_integration_test.go, gated by the "integration" build tag.
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
