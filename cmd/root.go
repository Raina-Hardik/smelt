package cmd

import (
	"os"
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
	if err := rootCmd.Execute(); err != nil {
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

	_ = viper.BindPFlag("smelt.log_level", rootCmd.PersistentFlags().Lookup("log-level"))
	_ = viper.BindPFlag("smelt.log_format", rootCmd.PersistentFlags().Lookup("log-format"))
}

func initLogger() {
	fi, err := os.Stdout.Stat()
	isTTY := err == nil && (fi.Mode()&os.ModeCharDevice) != 0
	if isTTY {
		log.Logger = zerolog.New(zerolog.ConsoleWriter{
			Out:        os.Stderr,
			TimeFormat: time.Kitchen,
		}).With().Timestamp().Logger()
	} else {
		log.Logger = zerolog.New(os.Stderr).With().Timestamp().Logger()
	}
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
