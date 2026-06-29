package config

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/Raina-Hardik/smelt/internal/ffmpeg"
	"github.com/spf13/viper"
)

// Config holds the resolved configuration for a smelt run.
// Populated by Load() from viper's merged view of flags, env vars, and config.yaml.
type Config struct {
	// smelt section
	Workers   int
	LogLevel  string
	LogFormat string

	// transcode section
	Src            string
	Ext            []string
	Codec          string
	CRF            int
	Preset         string
	AudioCodec     string // audio codec alias or "copy" (default copy = stream-copy)
	AudioBitrate   string // e.g. "192k"; applied only when re-encoding audio
	HWAccel        string // auto|none|nvenc|qsv|vaapi|amf|videotoolbox
	InPlace        bool
	SkipHardlinked   bool     // --inplace: skip files hardlinked elsewhere (avoid breaking the link)
	SkipSourceCodecs []string // skip files whose current video codec is in this list (e.g. av1)
	Force          bool   // re-transcode even when the output already exists
	Container      string // --to: target container/format (e.g. mp4); empty keeps source
	OutputDir      string
	Suffix         string
	Profile        string
	ExtraArgs      []string // raw ffmpeg passthrough args (--ffmpeg-arg + profile extra_args)

	SubtitleMode string // "copy" (default) | "drop"
	DBPath       string // path to the SQLite history database; "" disables the DB

	// CLI-only flags (not in config.yaml)
	DryRun    bool
	AssumeYes bool // -y: skip destructive-action confirmation prompts
}

// Load reads the current viper state and returns a fully resolved Config.
func Load() *Config {
	profileExtra := applyProfile()

	workers := viper.GetInt("smelt.workers")
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	suffix := viper.GetString("transcode.suffix")
	if suffix == "" {
		suffix = ".smelt"
	}

	hwaccel := viper.GetString("transcode.hwaccel")
	if hwaccel == "" {
		hwaccel = "auto"
	}

	audioCodec := viper.GetString("transcode.audio_codec")
	if audioCodec == "" {
		audioCodec = "copy"
	}

	return &Config{
		Workers:   workers,
		LogLevel:  viper.GetString("smelt.log_level"),
		LogFormat: viper.GetString("smelt.log_format"),

		Src:            viper.GetString("transcode.src"),
		Ext:            viper.GetStringSlice("transcode.ext"),
		Codec:          viper.GetString("transcode.codec"),
		CRF:            viper.GetInt("transcode.crf"),
		Preset:         viper.GetString("transcode.preset"),
		AudioCodec:     audioCodec,
		AudioBitrate:   viper.GetString("transcode.audio_bitrate"),
		HWAccel:        hwaccel,
		InPlace:        viper.GetBool("transcode.inplace"),
		SkipHardlinked:   viper.GetBool("transcode.skip_hardlinked"),
		SkipSourceCodecs: viper.GetStringSlice("transcode.skip_source_codecs"),
		Force:          viper.GetBool("transcode.force"),
		Container:      strings.TrimPrefix(viper.GetString("transcode.to"), "."),
		OutputDir:      viper.GetString("transcode.output_dir"),
		Suffix:         suffix,
		Profile:        viper.GetString("transcode.profile"),
		// Profile extra_args come first; CLI --ffmpeg-arg refine/append after.
		ExtraArgs: append(profileExtra, viper.GetStringSlice("transcode.ffmpeg_args")...),

		SubtitleMode: viper.GetString("transcode.subs"),
		DBPath:       viper.GetString("smelt.db"),

		DryRun:    viper.GetBool("transcode.dry_run"),
		AssumeYes: viper.GetBool("smelt.assume_yes"),
	}
}

// applyProfile loads the named --profile from the `profiles` map and registers
// its codec/crf/preset as viper *defaults*. Because a bound flag only wins in
// viper when explicitly changed, this gives the intended precedence: explicit
// flag > profile > built-in default. Returns the profile's extra_args.
func applyProfile() []string {
	name := viper.GetString("transcode.profile")
	if name == "" {
		return nil
	}
	base := "profiles." + name
	if !viper.IsSet(base) {
		return nil // unknown profile is reported by Config.Validate
	}
	if v := viper.GetString(base + ".codec"); v != "" {
		viper.SetDefault("transcode.codec", v)
	}
	if viper.IsSet(base + ".crf") {
		viper.SetDefault("transcode.crf", viper.GetInt(base+".crf"))
	}
	if v := viper.GetString(base + ".preset"); v != "" {
		viper.SetDefault("transcode.preset", v)
	}
	return viper.GetStringSlice(base + ".extra_args")
}

// Validate checks for mutually inconsistent configuration.
func (c *Config) Validate() error {
	if c.InPlace && c.OutputDir != "" {
		return errors.New("--inplace and --output-dir are mutually exclusive")
	}
	if c.InPlace && c.Container != "" {
		return errors.New("--inplace and --to (container change) are mutually exclusive")
	}
	if c.Profile != "" && !viper.IsSet("profiles."+c.Profile) {
		return fmt.Errorf("unknown profile %q", c.Profile)
	}
	if !ffmpeg.IsKnownCodec(c.Codec) {
		return fmt.Errorf("unknown codec %q; valid: %s", c.Codec, strings.Join(ffmpeg.KnownCodecs(), ", "))
	}
	if c.CRF < 0 || c.CRF > 51 {
		return fmt.Errorf("--crf %d out of range (0-51)", c.CRF)
	}
	if !ffmpeg.IsKnownHWAccel(c.HWAccel) {
		return fmt.Errorf("unknown --hwaccel %q; valid: auto, none, nvenc, qsv, vaapi, amf, videotoolbox", c.HWAccel)
	}
	if c.AudioCodec != "" && !ffmpeg.IsKnownAudioCodec(c.AudioCodec) { // "" == copy (Load defaults it)
		return fmt.Errorf("unknown --audio-codec %q; valid: %s", c.AudioCodec, strings.Join(ffmpeg.KnownAudioCodecs(), ", "))
	}
	switch strings.ToLower(c.SubtitleMode) {
	case "", "copy", "drop":
	default:
		return fmt.Errorf("unknown --subs %q; valid: copy, drop", c.SubtitleMode)
	}
	return nil
}
