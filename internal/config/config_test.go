package config

import (
	"reflect"
	"testing"

	"github.com/spf13/viper"
)

func setWebProfile() {
	viper.Set("profiles.web.codec", "h264")
	viper.Set("profiles.web.crf", 28)
	viper.Set("profiles.web.preset", "fast")
	viper.Set("profiles.web.extra_args", []string{"-movflags", "+faststart"})
}

func TestLoadAppliesProfile(t *testing.T) {
	viper.Reset()
	setWebProfile()
	viper.Set("transcode.profile", "web")

	cfg := Load()

	if cfg.Codec != "h264" || cfg.CRF != 28 || cfg.Preset != "fast" {
		t.Errorf("profile not applied: codec=%q crf=%d preset=%q", cfg.Codec, cfg.CRF, cfg.Preset)
	}
	if !reflect.DeepEqual(cfg.ExtraArgs, []string{"-movflags", "+faststart"}) {
		t.Errorf("extra_args = %v, want [-movflags +faststart]", cfg.ExtraArgs)
	}
}

func TestExplicitFlagOverridesProfile(t *testing.T) {
	viper.Reset()
	setWebProfile()
	viper.Set("transcode.profile", "web")
	// viper.Set has override precedence, the same tier an explicit --codec occupies.
	viper.Set("transcode.codec", "av1")

	cfg := Load()

	if cfg.Codec != "av1" {
		t.Errorf("explicit codec should beat profile: got %q, want av1", cfg.Codec)
	}
	if cfg.Preset != "fast" {
		t.Errorf("unset preset should still come from profile: got %q, want fast", cfg.Preset)
	}
}

func TestProfileExtraArgsPrecedeCLIArgs(t *testing.T) {
	viper.Reset()
	setWebProfile()
	viper.Set("transcode.profile", "web")
	viper.Set("transcode.ffmpeg_args", []string{"-vf", "scale=1280:-2"})

	cfg := Load()

	want := []string{"-movflags", "+faststart", "-vf", "scale=1280:-2"}
	if !reflect.DeepEqual(cfg.ExtraArgs, want) {
		t.Errorf("ExtraArgs = %v, want %v", cfg.ExtraArgs, want)
	}
}

func TestValidateRejectsUnknownProfile(t *testing.T) {
	viper.Reset()
	cfg := &Config{Profile: "nope"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for unknown profile")
	}

	viper.Reset()
	setWebProfile()
	known := &Config{Profile: "web", Codec: "h265"}
	if err := known.Validate(); err != nil {
		t.Errorf("known profile should validate: %v", err)
	}
}

func TestValidateRejectsBadCodecAndCRF(t *testing.T) {
	viper.Reset()
	if err := (&Config{Codec: "h256"}).Validate(); err == nil {
		t.Error("expected unknown codec h256 to be rejected")
	}
	if err := (&Config{Codec: "h265", CRF: 60}).Validate(); err == nil {
		t.Error("expected --crf 60 to be rejected")
	}
	if err := (&Config{Codec: "h265", CRF: 23}).Validate(); err != nil {
		t.Errorf("valid config should pass: %v", err)
	}
}

func TestValidateRejectsBadAudioCodec(t *testing.T) {
	viper.Reset()
	if err := (&Config{Codec: "h265", AudioCodec: "mp9"}).Validate(); err == nil {
		t.Error("expected unknown audio codec to be rejected")
	}
	if err := (&Config{Codec: "h265", AudioCodec: "opus"}).Validate(); err != nil {
		t.Errorf("opus should be valid: %v", err)
	}
	if err := (&Config{Codec: "h265"}).Validate(); err != nil { // empty == copy
		t.Errorf("empty audio codec should be valid: %v", err)
	}
}

func TestValidateRejectsInplaceWithOutputDir(t *testing.T) {
	viper.Reset()
	cfg := &Config{InPlace: true, OutputDir: "/out"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected --inplace + --output-dir to be rejected")
	}
}
