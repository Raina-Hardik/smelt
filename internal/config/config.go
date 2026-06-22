package config

import (
	"errors"
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
		ExtraArgs: viper.GetStringSlice("transcode.ffmpeg_args"),

		DryRun: viper.GetBool("transcode.dry_run"),
	}
}

// Validate checks for mutually inconsistent configuration.
func (c *Config) Validate() error {
	if c.InPlace && c.OutputDir != "" {
		return errors.New("--inplace and --output-dir are mutually exclusive")
	}
	return nil
}
