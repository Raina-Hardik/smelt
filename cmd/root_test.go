package cmd

import (
	"testing"

	"github.com/rs/zerolog"
)

func TestConfigureLoggerLevel(t *testing.T) {
	cases := map[string]zerolog.Level{
		"debug": zerolog.DebugLevel,
		"info":  zerolog.InfoLevel,
		"warn":  zerolog.WarnLevel,
		"error": zerolog.ErrorLevel,
		"":      zerolog.InfoLevel, // empty falls back to info
		"bogus": zerolog.InfoLevel, // unknown falls back to info
		"WARN":  zerolog.WarnLevel, // case-insensitive
	}
	for in, want := range cases {
		configureLogger(in, "json")
		if got := zerolog.GlobalLevel(); got != want {
			t.Errorf("configureLogger(%q): global level = %v, want %v", in, got, want)
		}
	}
	configureLogger("info", "auto") // restore default
}
