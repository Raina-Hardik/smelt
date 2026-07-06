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
	Src              string
	Ext              []string
	Codec            string
	CRF              int
	Preset           string
	AudioCodec       string // audio codec alias or "copy" (default copy = stream-copy)
	AudioBitrate     string // e.g. "192k"; applied only when re-encoding audio
	HWAccel          string // auto|none|nvenc|qsv|vaapi|amf|videotoolbox
	HWDecode         string // auto|off; hw decode on the resolved encoder's device when probed decodable
	InPlace          bool
	SkipHardlinked   bool     // --inplace: skip files hardlinked elsewhere (avoid breaking the link)
	SkipSourceCodecs []string // skip files whose current video codec is in this list (e.g. av1)
	Force            bool     // re-transcode even when the output already exists
	Container        string   // --to: target container/format (e.g. mp4); empty keeps source
	OutputDir        string
	Suffix           string
	Profile          string
	ExtraArgs        []string // raw ffmpeg passthrough args (--ffmpeg-arg + profile extra_args)
	DecodeThreads    int      // --decode-threads: caps decoder thread count (0 = ffmpeg default)
	AllowHDRLoss     bool     // --i-know-this-drops-hdr: required to transcode a detected Dolby Vision source

	SubtitleMode string // "copy" (default) | "drop"
	DBPath       string // path to the SQLite history database; "" disables the DB

	// CLI-only flags (not in config.yaml)
	DryRun    bool
	AssumeYes bool   // -y: skip destructive-action confirmation prompts
	RunID     string // --run-id: ties this run to a dashboard tracking row in the DB
}

// Defaults returns a *Config populated with the same built-in defaults that
// Load() applies when no flags or config file are present. Use this as the
// starting point when constructing a Config programmatically (e.g. in the
// server) rather than calling Load(), which reads global viper state.
// Call Validate() on the result after filling in the fields you care about.
func Defaults() *Config {
	return &Config{
		Workers:      runtime.NumCPU(),
		Suffix:       ".smelt",
		HWAccel:      "auto",
		HWDecode:     "auto",
		AudioCodec:   "copy",
		SubtitleMode: "copy",
		LogLevel:     "info",
		LogFormat:    "auto",
	}
}

// getStringSlice wraps viper.GetStringSlice with comma-splitting for values
// sourced from a flat SMELT_ env var (e.g. SMELT_EXT=mkv,mp4,avi). Viper's
// cast never splits a plain string on commas — flags and config.yaml lists
// already arrive as multiple elements, so this only ever fires for the env
// path, and only when it wouldn't otherwise change a legitimate single value.
func getStringSlice(key string) []string {
	vals := viper.GetStringSlice(key)
	if len(vals) == 1 && strings.Contains(vals[0], ",") {
		parts := strings.Split(vals[0], ",")
		for i, p := range parts {
			parts[i] = strings.TrimSpace(p)
		}
		return parts
	}
	return vals
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

	hwdecode := viper.GetString("transcode.hwdecode")
	if hwdecode == "" {
		hwdecode = "auto"
	}

	audioCodec := viper.GetString("transcode.audio_codec")
	if audioCodec == "" {
		audioCodec = "copy"
	}

	return &Config{
		Workers:   workers,
		LogLevel:  viper.GetString("smelt.log_level"),
		LogFormat: viper.GetString("smelt.log_format"),

		Src:              viper.GetString("transcode.src"),
		Ext:              getStringSlice("transcode.ext"),
		Codec:            viper.GetString("transcode.codec"),
		CRF:              viper.GetInt("transcode.crf"),
		Preset:           viper.GetString("transcode.preset"),
		AudioCodec:       audioCodec,
		AudioBitrate:     viper.GetString("transcode.audio_bitrate"),
		HWAccel:          hwaccel,
		HWDecode:         hwdecode,
		InPlace:          viper.GetBool("transcode.inplace"),
		SkipHardlinked:   viper.GetBool("transcode.skip_hardlinked"),
		SkipSourceCodecs: getStringSlice("transcode.skip_source_codecs"),
		Force:            viper.GetBool("transcode.force"),
		Container:        strings.TrimPrefix(viper.GetString("transcode.to"), "."),
		OutputDir:        viper.GetString("transcode.output_dir"),
		Suffix:           suffix,
		Profile:          viper.GetString("transcode.profile"),
		// Profile extra_args come first; CLI --ffmpeg-arg refine/append after.
		ExtraArgs:     append(profileExtra, viper.GetStringSlice("transcode.ffmpeg_args")...),
		DecodeThreads: viper.GetInt("transcode.decode_threads"),
		AllowHDRLoss:  viper.GetBool("transcode.allow_hdr_loss"),

		SubtitleMode: viper.GetString("transcode.subs"),
		DBPath:       viper.GetString("smelt.db"),

		DryRun:    viper.GetBool("transcode.dry_run"),
		AssumeYes: viper.GetBool("smelt.assume_yes"),
		RunID:     viper.GetString("smelt.run_id"),
	}
}

// applyProfile resolves the named --profile (built-in ∪ config.yaml) and
// registers each field it sets as a viper *default*. Because a bound flag only
// wins in viper when explicitly changed, this gives the intended precedence:
// explicit flag > profile > built-in default. Returns the profile's extra_args
// so Load can prepend them ahead of any CLI --ffmpeg-arg values.
func applyProfile() []string {
	name := viper.GetString("transcode.profile")
	if name == "" {
		return nil
	}
	p, ok := ResolveProfile(name)
	if !ok {
		return nil // unknown profile is reported by Config.Validate
	}
	if p.Codec != "" {
		viper.SetDefault("transcode.codec", p.Codec)
	}
	if p.CRF != nil {
		viper.SetDefault("transcode.crf", *p.CRF)
	}
	if p.Preset != "" {
		viper.SetDefault("transcode.preset", p.Preset)
	}
	if p.AudioCodec != "" {
		viper.SetDefault("transcode.audio_codec", p.AudioCodec)
	}
	if p.AudioBitrate != "" {
		viper.SetDefault("transcode.audio_bitrate", p.AudioBitrate)
	}
	if p.Container != "" {
		viper.SetDefault("transcode.to", p.Container)
	}
	if p.Subs != "" {
		viper.SetDefault("transcode.subs", p.Subs)
	}
	return p.ExtraArgs
}

// Validate checks for mutually inconsistent configuration.
func (c *Config) Validate() error {
	if c.InPlace && c.OutputDir != "" {
		return errors.New("--inplace and --output-dir are mutually exclusive")
	}
	if c.InPlace && c.Container != "" {
		if c.Profile != "" {
			return fmt.Errorf("--inplace and --to (container change) are mutually exclusive; profile %q sets --to %s (clear it with --to \"\" or drop --inplace)", c.Profile, c.Container)
		}
		return errors.New("--inplace and --to (container change) are mutually exclusive")
	}
	if c.Profile != "" && !IsKnownProfile(c.Profile) {
		return fmt.Errorf("unknown profile %q; run `smelt profiles` to list built-in and configured profiles", c.Profile)
	}
	if !ffmpeg.IsKnownCodec(c.Codec) {
		return fmt.Errorf("unknown codec %q; valid: %s", c.Codec, strings.Join(ffmpeg.KnownCodecs(), ", "))
	}
	if c.CRF < 0 || c.CRF > 51 {
		return fmt.Errorf("--crf %d out of range (0-51)", c.CRF)
	}
	if c.DecodeThreads < 0 {
		return fmt.Errorf("--decode-threads %d must be >= 0", c.DecodeThreads)
	}
	if !ffmpeg.IsKnownHWAccel(c.HWAccel) {
		return fmt.Errorf("unknown --hwaccel %q; valid: auto, none, nvenc, qsv, vaapi, amf, videotoolbox", c.HWAccel)
	}
	switch strings.ToLower(c.HWDecode) {
	case "", "auto", "off": // "" == auto (Load defaults it)
	default:
		return fmt.Errorf("unknown --hwdecode %q; valid: auto, off", c.HWDecode)
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
