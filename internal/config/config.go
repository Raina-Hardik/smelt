package config

import (
	"runtime"

	"github.com/spf13/viper"
)

type Config struct {
	Src        string
	Extensions []string
	Codec      string
	Workers    int
	InPlace    bool
	DryRun     bool
	LogLevel   string
}

func Load() *Config {
	workers := viper.GetInt("workers")
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	return &Config{
		Src:        viper.GetString("src"),
		Extensions: viper.GetStringSlice("ext"),
		Codec:      viper.GetString("codec"),
		Workers:    workers,
		InPlace:    viper.GetBool("inplace"),
		DryRun:     viper.GetBool("dry_run"),
		LogLevel:   viper.GetString("log_level"),
	}
}
