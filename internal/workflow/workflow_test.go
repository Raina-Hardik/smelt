package workflow

import (
	"strings"
	"testing"
	"time"

	"github.com/Raina-Hardik/smelt/internal/config"
)

func baseCfg() *config.Config {
	return &config.Config{
		Src: "/mnt/media", Ext: []string{"mkv", "mp4"}, Codec: "h265", CRF: 23,
		Preset: "medium", HWAccel: "auto", Workers: 8, Suffix: ".smelt", AudioCodec: "copy",
	}
}

func TestScriptCoreShape(t *testing.T) {
	s := Script(baseCfg(), Options{Name: "nightly", Binary: "/usr/bin/smelt", Now: time.Unix(0, 0).UTC()})
	wants := []string{
		"#!/bin/sh",
		"# smelt workflow: nightly",
		"set -eu",
		"flock -n 9",                 // overlap guard
		"'/usr/bin/smelt' transcode", // quoted binary
		"--src '/mnt/media'",
		"--ext 'mkv,mp4'",
		"--codec 'h265'",
		"--crf '23'",
		"--audio-codec 'copy'",
	}
	for _, w := range wants {
		if !strings.Contains(s, w) {
			t.Errorf("script missing %q\n---\n%s", w, s)
		}
	}
}

func TestScriptConditionalFlags(t *testing.T) {
	cfg := baseCfg()
	cfg.InPlace = true
	cfg.SkipHardlinked = true
	cfg.Force = true
	cfg.Container = "mp4"
	cfg.OutputDir = "" // inplace + output-dir are mutually exclusive; leave unset
	cfg.AudioBitrate = "192k"
	cfg.ExtraArgs = []string{"-vf", "scale=1280:-2"}

	s := Script(cfg, Options{})
	for _, w := range []string{
		"--inplace", "--skip-hardlinked", "--force", "--to 'mp4'",
		"--audio-bitrate '192k'", "--ffmpeg-arg '-vf'", "--ffmpeg-arg 'scale=1280:-2'",
		"--assume-yes", // inplace must run non-interactively
	} {
		if !strings.Contains(s, w) {
			t.Errorf("script missing %q", w)
		}
	}

	// A non-inplace script must not silently assume-yes.
	if strings.Contains(Script(baseCfg(), Options{}), "--assume-yes") {
		t.Error("non-inplace workflow should not include --assume-yes")
	}
}

func TestShellQuoteEscapes(t *testing.T) {
	cases := map[string]string{
		"plain":      "'plain'",
		"a b":        "'a b'",
		"it's":       `'it'\''s'`,
		"/mnt/x y/z": "'/mnt/x y/z'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCrontabLine(t *testing.T) {
	got := CrontabLine("/opt/wf/nightly.sh", "0 3 * * *")
	want := "0 3 * * * '/opt/wf/nightly.sh' >> /opt/wf/nightly.sh.log 2>&1"
	if got != want {
		t.Errorf("CrontabLine = %q, want %q", got, want)
	}
}

func TestSanitize(t *testing.T) {
	if got := sanitize("my media/run #1"); got != "my-media-run--1" {
		t.Errorf("sanitize = %q", got)
	}
}
