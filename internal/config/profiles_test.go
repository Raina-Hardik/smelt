package config

import (
	"reflect"
	"testing"

	"github.com/spf13/viper"
)

func TestBuiltinProfileAppliesWithoutConfig(t *testing.T) {
	viper.Reset()
	viper.Set("transcode.profile", "web")

	cfg := Load()

	if cfg.Codec != "h264" {
		t.Errorf("codec = %q, want h264", cfg.Codec)
	}
	if cfg.CRF != 23 {
		t.Errorf("crf = %d, want 23", cfg.CRF)
	}
	if cfg.Preset != "fast" {
		t.Errorf("preset = %q, want fast", cfg.Preset)
	}
	if cfg.AudioCodec != "aac" {
		t.Errorf("audio codec = %q, want aac", cfg.AudioCodec)
	}
	if cfg.AudioBitrate != "160k" {
		t.Errorf("audio bitrate = %q, want 160k", cfg.AudioBitrate)
	}
	if cfg.Container != "mp4" {
		t.Errorf("container = %q, want mp4", cfg.Container)
	}
	if !reflect.DeepEqual(cfg.ExtraArgs, []string{"-movflags", "+faststart"}) {
		t.Errorf("extra_args = %v, want [-movflags +faststart]", cfg.ExtraArgs)
	}
}

func TestBuiltinProfileValidatesWithoutConfig(t *testing.T) {
	viper.Reset()
	cfg := &Config{Profile: "archive", Codec: "h265"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("built-in profile should validate with no config file: %v", err)
	}
}

func TestConfigOverridesBuiltinFieldByField(t *testing.T) {
	viper.Reset()
	// User customises only crf on the built-in web profile; every other field
	// (audio, container, extra_args) must still come from the built-in.
	viper.Set("profiles.web.crf", 30)
	viper.Set("transcode.profile", "web")

	cfg := Load()

	if cfg.CRF != 30 {
		t.Errorf("crf = %d, want 30 (config override)", cfg.CRF)
	}
	if cfg.Codec != "h264" {
		t.Errorf("codec = %q, want h264 (from built-in)", cfg.Codec)
	}
	if cfg.AudioCodec != "aac" {
		t.Errorf("audio codec = %q, want aac (from built-in)", cfg.AudioCodec)
	}
	if cfg.Container != "mp4" {
		t.Errorf("container = %q, want mp4 (from built-in)", cfg.Container)
	}
}

func TestExplicitFlagBeatsBuiltinProfile(t *testing.T) {
	viper.Reset()
	viper.Set("transcode.profile", "archive")
	viper.Set("transcode.crf", 10) // override tier == explicit --crf

	cfg := Load()

	if cfg.CRF != 10 {
		t.Errorf("explicit crf should beat profile: got %d, want 10", cfg.CRF)
	}
	if cfg.Codec != "h265" {
		t.Errorf("unset codec should come from profile: got %q, want h265", cfg.Codec)
	}
}

func TestResolveProfile(t *testing.T) {
	viper.Reset()

	if _, ok := ResolveProfile("av1"); !ok {
		t.Error("av1 built-in should resolve")
	}
	if _, ok := ResolveProfile("does-not-exist"); ok {
		t.Error("unknown profile should not resolve")
	}
	if _, ok := ResolveProfile(""); ok {
		t.Error("empty name should not resolve")
	}

	viper.Set("profiles.custom.codec", "vp9")
	p, ok := ResolveProfile("custom")
	if !ok || p.Codec != "vp9" {
		t.Errorf("config-only profile = %+v, ok=%v; want codec vp9", p, ok)
	}
}

func TestProfileNamesIncludesBuiltinsAndConfig(t *testing.T) {
	viper.Reset()
	viper.Set("profiles.custom.codec", "vp9")

	names := ProfileNames()
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	for _, want := range []string{"web", "web-hevc", "archive", "av1", "mobile", "custom"} {
		if !seen[want] {
			t.Errorf("ProfileNames missing %q; got %v", want, names)
		}
	}
	// Sorted and deduplicated.
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Errorf("ProfileNames not sorted/unique: %v", names)
			break
		}
	}
}

func TestHasConfigProfile(t *testing.T) {
	viper.Reset()
	viper.Set("profiles.custom.codec", "vp9")

	if !HasConfigProfile("custom") {
		t.Error("custom should be a config profile")
	}
	if HasConfigProfile("web") {
		t.Error("web is built-in only here; should not report as a config profile")
	}
}
