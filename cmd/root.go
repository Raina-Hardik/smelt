package cmd

import (
	"context"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "smelt",
	Short: "Highly parallel ffmpeg-powered media transcoder",
	Long: `Smelt is a highly parallel, ffmpeg-powered media transcoding CLI and TUI.

It scans a source directory, applies configured codec targets, and transcodes
files concurrently — with live progress in the TUI or structured log output
in daemon and pipe mode.`,
	SilenceUsage: true,
}

// Execute is the entry point called from main.go.
func Execute() {
	initLogger()

	// Signal-aware context: SIGINT/SIGTERM cancels the context, which propagates
	// down to in-flight ffmpeg children via exec.CommandContext.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(
		&cfgFile, "config", "",
		"path to config file; searches ./config.yaml then $HOME/.config/smelt/config.yaml",
	)
	rootCmd.PersistentFlags().String(
		"log-level", "info",
		"log level: debug|info|warn|error",
	)
	rootCmd.PersistentFlags().String(
		"log-format", "auto",
		"log output format: auto|json|pretty",
	)
	rootCmd.PersistentFlags().BoolP(
		"assume-yes", "y", false,
		"skip confirmation prompts (assume yes) for destructive actions",
	)

	_ = viper.BindPFlag("smelt.log_level", rootCmd.PersistentFlags().Lookup("log-level"))
	_ = viper.BindPFlag("smelt.log_format", rootCmd.PersistentFlags().Lookup("log-format"))
	_ = viper.BindPFlag("smelt.assume_yes", rootCmd.PersistentFlags().Lookup("assume-yes"))
}

// initLogger installs a sensible default before flags/config are resolved.
// configureLogger is called again from the command path once *Config is loaded.
func initLogger() { configureLogger("info", "auto") }

// configureLogger reconfigures the global zerolog logger from resolved config.
// level: debug|info|warn|error (unknown → info).
// format: auto (pretty when stderr is a TTY) | json | pretty.
func configureLogger(level, format string) {
	lvl, err := zerolog.ParseLevel(strings.ToLower(level))
	if err != nil || level == "" {
		lvl = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(lvl)

	var pretty bool
	switch strings.ToLower(format) {
	case "json":
		pretty = false
	case "pretty":
		pretty = true
	default: // auto
		pretty = stderrIsTTY()
	}

	var w io.Writer = os.Stderr
	if pretty {
		w = zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.Kitchen}
	}
	log.Logger = zerolog.New(w).With().Timestamp().Logger()
}

func stderrIsTTY() bool {
	fi, err := os.Stderr.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
		viper.AddConfigPath(".")
		viper.AddConfigPath("$HOME/.config/smelt")
	}
	viper.SetEnvPrefix("SMELT")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, notFound := err.(viper.ConfigFileNotFoundError); !notFound {
			log.Warn().Err(err).Msg("could not read config file")
		}
	}
}
