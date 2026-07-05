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

func TestGetStringSliceSplitsEnvCommaValue(t *testing.T) {
	viper.Reset()
	_ = viper.BindEnv("transcode.ext", "SMELT_EXT")
	t.Setenv("SMELT_EXT", "mkv,mp4,avi")

	got := getStringSlice("transcode.ext")
	want := []string{"mkv", "mp4", "avi"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("getStringSlice = %v, want %v", got, want)
	}
}

func TestGetStringSlicePassesThroughConfigList(t *testing.T) {
	viper.Reset()
	viper.Set("transcode.ext", []string{"mkv", "mp4"})

	got := getStringSlice("transcode.ext")
	want := []string{"mkv", "mp4"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("getStringSlice = %v, want %v", got, want)
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

func TestConfigFileValueOverridesProfile(t *testing.T) {
	viper.Reset()
	setWebProfile()
	viper.Set("transcode.profile", "web")
	// MergeConfigMap lands values in viper's config-file tier, distinct from
	// the override tier viper.Set uses — this is what a real config.yaml
	// value occupies, and it must still beat the profile's SetDefault.
	if err := viper.MergeConfigMap(map[string]any{
		"transcode": map[string]any{"codec": "av1"},
	}); err != nil {
		t.Fatalf("MergeConfigMap: %v", err)
	}

	cfg := Load()

	if cfg.Codec != "av1" {
		t.Errorf("config.yaml codec should beat profile: got %q, want av1", cfg.Codec)
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

func TestValidateRejectsBadHWDecode(t *testing.T) {
	viper.Reset()
	if err := (&Config{Codec: "h265", HWDecode: "on"}).Validate(); err == nil {
		t.Error("expected unknown hwdecode value to be rejected")
	}
	for _, v := range []string{"", "auto", "off", "AUTO"} {
		if err := (&Config{Codec: "h265", HWDecode: v}).Validate(); err != nil {
			t.Errorf("hwdecode %q should be valid: %v", v, err)
		}
	}
}

func TestValidateRejectsInplaceWithOutputDir(t *testing.T) {
	viper.Reset()
	cfg := &Config{InPlace: true, OutputDir: "/out"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected --inplace + --output-dir to be rejected")
	}
}

// TestDirectConstruction documents the server construction path: build a
// *Config from a struct literal (or Defaults) + field assignment, then call
// Validate(). No viper involvement — Validate() only calls viper.IsSet when
// Profile != "", which the server never sets in v1.
func TestDirectConstruction(t *testing.T) {
	viper.Reset() // confirm no viper state is read

	cfg := Defaults()
	cfg.Src = "/mnt/media"
	cfg.Ext = []string{"mkv", "mp4"}
	cfg.Codec = "h265"
	cfg.CRF = 23

	if err := cfg.Validate(); err != nil {
		t.Errorf("server-constructed config should validate cleanly: %v", err)
	}
	if cfg.Workers <= 0 {
		t.Error("Defaults() should populate Workers from runtime.NumCPU")
	}
	if cfg.AudioCodec != "copy" {
		t.Errorf("Defaults() AudioCodec = %q, want copy", cfg.AudioCodec)
	}
}

func TestDefaultsMatchLoadDefaults(t *testing.T) {
	viper.Reset()
	fromDefaults := Defaults()
	fromLoad := Load()

	// These fields must agree — they are the documented defaults in both paths.
	if fromDefaults.HWAccel != fromLoad.HWAccel {
		t.Errorf("HWAccel: Defaults=%q Load=%q", fromDefaults.HWAccel, fromLoad.HWAccel)
	}
	if fromDefaults.AudioCodec != fromLoad.AudioCodec {
		t.Errorf("AudioCodec: Defaults=%q Load=%q", fromDefaults.AudioCodec, fromLoad.AudioCodec)
	}
	if fromDefaults.Suffix != fromLoad.Suffix {
		t.Errorf("Suffix: Defaults=%q Load=%q", fromDefaults.Suffix, fromLoad.Suffix)
	}
}
