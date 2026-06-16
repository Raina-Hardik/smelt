package cmd

import (
	"os"
	"runtime"
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
	Long: `smelt scans a media library directory and transcodes files using
ffmpeg, with configurable concurrency, codec targets, and output modes.`,
	SilenceUsage: true,
}

func Execute() {
	initLogger()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ./config.yaml)")
	rootCmd.PersistentFlags().String("log-level", "info", "log level: trace|debug|info|warn|error")
	rootCmd.PersistentFlags().String("src", ".", "source directory to scan")
	rootCmd.PersistentFlags().StringSlice("ext", []string{"mkv", "mp4"}, "file extensions to match")
	rootCmd.PersistentFlags().String("codec", "h265", "target video codec (h265, h264, av1, vp9)")
	rootCmd.PersistentFlags().Int("workers", runtime.NumCPU(), "maximum parallel transcoding jobs")
	rootCmd.PersistentFlags().Bool("inplace", false, "replace original file after successful transcode")

	_ = viper.BindPFlag("log_level", rootCmd.PersistentFlags().Lookup("log-level"))
	_ = viper.BindPFlag("src", rootCmd.PersistentFlags().Lookup("src"))
	_ = viper.BindPFlag("ext", rootCmd.PersistentFlags().Lookup("ext"))
	_ = viper.BindPFlag("codec", rootCmd.PersistentFlags().Lookup("codec"))
	_ = viper.BindPFlag("workers", rootCmd.PersistentFlags().Lookup("workers"))
	_ = viper.BindPFlag("inplace", rootCmd.PersistentFlags().Lookup("inplace"))
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
