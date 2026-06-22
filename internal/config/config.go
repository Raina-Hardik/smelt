package config

import (
	"errors"
	"fmt"
	"runtime"

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
	Src       string
	Ext       []string
	Codec     string
	CRF       int
	Preset    string
	InPlace   bool
	OutputDir string
	Suffix    string
	Profile   string
	ExtraArgs []string // raw ffmpeg passthrough args (--ffmpeg-arg + profile extra_args)

	// CLI-only flags (not in config.yaml)
	DryRun bool
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

	return &Config{
		Workers:   workers,
		LogLevel:  viper.GetString("smelt.log_level"),
		LogFormat: viper.GetString("smelt.log_format"),

		Src:       viper.GetString("transcode.src"),
		Ext:       viper.GetStringSlice("transcode.ext"),
		Codec:     viper.GetString("transcode.codec"),
		CRF:       viper.GetInt("transcode.crf"),
		Preset:    viper.GetString("transcode.preset"),
		InPlace:   viper.GetBool("transcode.inplace"),
		OutputDir: viper.GetString("transcode.output_dir"),
		Suffix:    suffix,
		Profile:   viper.GetString("transcode.profile"),
		// Profile extra_args come first; CLI --ffmpeg-arg refine/append after.
		ExtraArgs: append(profileExtra, viper.GetStringSlice("transcode.ffmpeg_args")...),

		DryRun: viper.GetBool("transcode.dry_run"),
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
	if c.Profile != "" && !viper.IsSet("profiles."+c.Profile) {
		return fmt.Errorf("unknown profile %q", c.Profile)
	}
	return nil
}
