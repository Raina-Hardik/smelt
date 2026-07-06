package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func runProfilesCmd(t *testing.T, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	rootCmd.SetArgs(args)
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return buf.String()
}

func TestProfilesListShowsBuiltins(t *testing.T) {
	viper.Reset()
	t.Cleanup(func() { viper.Reset() })

	out := runProfilesCmd(t, "profiles")
	for _, name := range []string{"web", "web-hevc", "archive", "av1", "mobile"} {
		if !strings.Contains(out, name) {
			t.Errorf("profiles list missing built-in %q\n%s", name, out)
		}
	}
	if !strings.Contains(out, "built-in") {
		t.Errorf("profiles list should mark built-in source\n%s", out)
	}
}

func TestProfilesListMarksOverride(t *testing.T) {
	viper.Reset()
	t.Cleanup(func() { viper.Reset() })
	viper.Set("profiles.web.crf", 30)

	out := runProfilesCmd(t, "profiles")
	if !strings.Contains(out, "overridden") {
		t.Errorf("expected web to be marked overridden\n%s", out)
	}
}

func TestProfilesShowExpandsFlags(t *testing.T) {
	viper.Reset()
	t.Cleanup(func() { viper.Reset() })

	out := runProfilesCmd(t, "profiles", "show", "web")
	for _, want := range []string{
		"--codec h264", "--crf 23", "--preset fast",
		"--audio-codec aac", "--to mp4",
		"--ffmpeg-arg -movflags", "--ffmpeg-arg +faststart",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("profiles show web missing %q\n%s", want, out)
		}
	}
}

func TestProfileFlagCompletion(t *testing.T) {
	viper.Reset()
	t.Cleanup(func() { viper.Reset() })

	// cobra's hidden __complete command drives shell completion; exercising it
	// verifies the --profile completion func is wired to the profile registry.
	out := runProfilesCmd(t, "__complete", "transcode", "--profile", "")
	for _, name := range []string{"web", "archive", "av1"} {
		if !strings.Contains(out, name) {
			t.Errorf("--profile completion missing %q\n%s", name, out)
		}
	}
}

func TestProfilesShowUnknownErrors(t *testing.T) {
	viper.Reset()
	t.Cleanup(func() { viper.Reset() })

	var buf bytes.Buffer
	rootCmd.SetArgs([]string{"profiles", "show", "nope"})
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	if err := rootCmd.Execute(); err == nil {
		t.Fatalf("expected error for unknown profile, got output:\n%s", buf.String())
	}
}
