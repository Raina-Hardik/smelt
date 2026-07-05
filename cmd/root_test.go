package cmd

import (
	"testing"

	"github.com/rs/zerolog"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
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

func TestBindEnvVarsCoversDocumentedKeys(t *testing.T) {
	viper.Reset()
	bindEnvVars()

	for key, env := range envVarBindings {
		t.Setenv(env, "probe-value")
		if got := viper.GetString(key); got != "probe-value" {
			t.Errorf("%s -> %s: viper.GetString(%q) = %q, want %q", env, key, key, got, "probe-value")
		}
	}
}

func TestEnvVarPrecedenceBelowFlagAboveConfig(t *testing.T) {
	viper.Reset()
	bindEnvVars()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("codec", "h265", "")
	_ = viper.BindPFlag("transcode.codec", fs.Lookup("codec"))
	viper.SetDefault("transcode.codec", "default-codec")

	if got := viper.GetString("transcode.codec"); got != "default-codec" {
		t.Fatalf("baseline default: got %q, want default-codec", got)
	}

	t.Setenv("SMELT_CODEC", "env-codec")
	if got := viper.GetString("transcode.codec"); got != "env-codec" {
		t.Errorf("env should override default: got %q, want env-codec", got)
	}

	_ = fs.Set("codec", "flag-codec")
	if got := viper.GetString("transcode.codec"); got != "flag-codec" {
		t.Errorf("explicit flag should override env: got %q, want flag-codec", got)
	}
}
