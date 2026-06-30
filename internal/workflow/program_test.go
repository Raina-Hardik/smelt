package workflow

import (
	"strings"
	"testing"
	"time"

	"github.com/Raina-Hardik/smelt/internal/config"
)

var testOpts = Options{
	Binary:  "/usr/bin/smelt",
	Version: "v0.11.0",
	Now:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
}

func TestRenderContainsManifestBlock(t *testing.T) {
	p := Program{
		Name: "nightly",
		Src:  "/mnt/media",
		Ext:  []string{"mkv", "mp4"},
		Rules: []Rule{
			{
				When: []Cond{{Field: "codec", Op: "ne", Value: "hevc"}},
				Do:   Action{Cmd: "transcode", Args: []string{"--codec", "h265", "--crf", "23"}},
			},
		},
	}

	out := Render(p, testOpts)

	if !strings.Contains(out, "# >>> smelt:manifest v1 >>>") {
		t.Error("missing manifest open marker")
	}
	if !strings.Contains(out, "# <<< smelt:manifest <<<") {
		t.Error("missing manifest close marker")
	}
	if !strings.Contains(out, "# src: /mnt/media") {
		t.Error("manifest missing src")
	}
	if !strings.Contains(out, "# rule: when codec ne hevc") {
		t.Error("manifest missing rule")
	}
	if !strings.Contains(out, "flock") {
		t.Error("missing flock overlap guard")
	}
	if !strings.Contains(out, "gum") {
		t.Error("missing gum affordance block")
	}
}

func TestParseRoundTrip(t *testing.T) {
	p := Program{
		Name:     "weekly",
		Schedule: "0 3 * * 0",
		Src:      "/mnt/movies",
		Ext:      []string{"mkv"},
		Rules: []Rule{
			{
				When: []Cond{
					{Field: "codec", Op: "ne", Value: "hevc"},
					{Field: "height", Op: "gt", Value: "1080"},
				},
				Do: Action{Cmd: "transcode"},
			},
			{
				When: []Cond{{Field: "codec", Op: "ne", Value: "hevc"}},
				Do:   Action{Cmd: "transcode"},
			},
			{
				Do: Action{Cmd: "skip"}, // catch-all
			},
		},
	}

	script := Render(p, testOpts)
	parsed, err := Parse(script)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if parsed.Name != p.Name {
		t.Errorf("Name: got %q want %q", parsed.Name, p.Name)
	}
	if parsed.Schedule != p.Schedule {
		t.Errorf("Schedule: got %q want %q", parsed.Schedule, p.Schedule)
	}
	if parsed.Src != p.Src {
		t.Errorf("Src: got %q want %q", parsed.Src, p.Src)
	}
	if len(parsed.Rules) != len(p.Rules) {
		t.Fatalf("Rules len: got %d want %d", len(parsed.Rules), len(p.Rules))
	}
	// First rule: two conditions
	if len(parsed.Rules[0].When) != 2 {
		t.Errorf("rule[0] conditions: got %d want 2", len(parsed.Rules[0].When))
	}
	// Catch-all last rule: no conditions
	if len(parsed.Rules[2].When) != 0 {
		t.Errorf("catch-all rule should have no conditions, got %d", len(parsed.Rules[2].When))
	}
}

func TestRenderEmitsRunLifecycle(t *testing.T) {
	p := Program{
		Name: "nightly",
		Src:  "/mnt/media",
		Ext:  []string{"mkv"},
		Rules: []Rule{
			{When: []Cond{{Field: "codec", Op: "ne", Value: "hevc"}}, Do: Action{Cmd: "transcode", Args: []string{"--codec", "h265"}}},
		},
	}
	out := Render(p, testOpts)

	if !strings.Contains(out, `RUN_ID="${SMELT_RUN_ID:-$(date +%s)-$$}"`) {
		t.Error("missing RUN_ID auto-generation")
	}
	if !strings.Contains(out, "each --src '/mnt/media'") || !strings.Contains(out, `--run-id "$RUN_ID"`) {
		t.Error("each invocation missing --run-id")
	}
	if !strings.Contains(out, `finish-run --run-id "$RUN_ID"`) {
		t.Error("missing finish-run line")
	}
	if !strings.Contains(out, "|| true") {
		t.Error("do branch missing '|| true' resilience guard")
	}
}

func TestRuleArgsRoundTrip(t *testing.T) {
	p := Program{
		Name: "p",
		Src:  "/m",
		Ext:  []string{"mkv"},
		Rules: []Rule{
			{
				When: []Cond{{Field: "codec", Op: "ne", Value: "hevc"}},
				Do:   Action{Cmd: "transcode", Args: []string{"--codec", "h265", "--crf", "24"}},
			},
		},
	}
	parsed, err := Parse(Render(p, testOpts))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := parsed.Rules[0].Do
	if got.Cmd != "transcode" {
		t.Errorf("Cmd = %q, want transcode", got.Cmd)
	}
	want := []string{"--codec", "h265", "--crf", "24"}
	if strings.Join(got.Args, " ") != strings.Join(want, " ") {
		t.Errorf("Args = %v, want %v", got.Args, want)
	}
}

func TestParseErrorOnMissingBlock(t *testing.T) {
	_, err := Parse("#!/bin/sh\n# no manifest here\n")
	if err == nil {
		t.Error("expected error for missing manifest block")
	}
}

func TestScriptBackwardCompat(t *testing.T) {
	cfg := config.Defaults()
	cfg.Src = "/mnt/media"
	cfg.Ext = []string{"mkv", "mp4"}
	cfg.Codec = "h265"
	cfg.CRF = 23

	// Script() must still produce a valid shell script with the transcode call.
	out := Script(cfg, testOpts)
	if !strings.Contains(out, "smelt transcode") && !strings.Contains(out, "transcode") {
		t.Error("Script() output missing transcode call")
	}
	// Must NOT contain the program manifest block (Script uses the old single-call format).
	if strings.Contains(out, "smelt:manifest") {
		t.Error("Script() should not emit a smelt:manifest block")
	}
}

func TestTranscodeArgsExported(t *testing.T) {
	cfg := config.Defaults()
	cfg.Src = "/mnt/media"
	cfg.Codec = "h265"
	cfg.CRF = 20

	args := TranscodeArgs(cfg)
	if len(args) == 0 {
		t.Error("TranscodeArgs returned empty slice")
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--codec") {
		t.Error("TranscodeArgs missing --codec flag")
	}
	if !strings.Contains(joined, "--crf") {
		t.Error("TranscodeArgs missing --crf flag")
	}
}
