package cmd

import (
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var transcodeCmd = &cobra.Command{
	Use:   "transcode",
	Short: "Transcode media files in a directory",
	Long: `Scan a source directory for media files matching the given extensions and
transcode each file using ffmpeg with the configured codec, CRF, and preset.
Jobs run in parallel up to --workers, with progress reported via zerolog.`,
	Example: `  smelt transcode --src /mnt/media --ext mkv,mp4 --codec h265 --workers 8
  smelt transcode --src /mnt/media --dry-run
  smelt transcode --src /mnt/media --inplace --codec h264 --crf 22 --preset fast
  smelt transcode --src /mnt/media --profile web
  smelt transcode --src /mnt/media --output-dir /mnt/transcoded`,
	RunE: runTranscode,
}

func init() {
	addTranscodeFlags(transcodeCmd)
	transcodeCmd.Flags().Bool(
		"dry-run", false,
		"print transcode plan without executing anything",
	)
	_ = viper.BindPFlag("transcode.dry_run", transcodeCmd.Flags().Lookup("dry-run"))

	rootCmd.AddCommand(transcodeCmd)
}

func runTranscode(cmd *cobra.Command, args []string) error {
	log.Info().Str("cmd", "transcode").Msg("not implemented")
	return nil
}

// addTranscodeFlags registers the flags shared between transcode and tui.
func addTranscodeFlags(cmd *cobra.Command) {
	cmd.Flags().String(
		"src", ".",
		"source directory to scan",
	)
	cmd.Flags().StringSlice(
		"ext", []string{"mkv", "mp4", "avi"},
		"file extensions to match",
	)
	cmd.Flags().String(
		"codec", "h265",
		"target video codec: h264|h265|av1|vp9",
	)
	cmd.Flags().Int(
		"crf", 23,
		"constant rate factor 0-51; lower = higher quality",
	)
	cmd.Flags().String(
		"preset", "medium",
		"ffmpeg encoding preset",
	)
	cmd.Flags().Int(
		"workers", 0,
		"maximum parallel transcode jobs; 0 = runtime.NumCPU()",
	)
	cmd.Flags().Bool(
		"inplace", false,
		"replace original file after successful transcode",
	)
	cmd.Flags().String(
		"output-dir", "",
		"write output files to this directory instead of alongside source",
	)
	cmd.Flags().String(
		"profile", "",
		"named profile from config; overrides --codec, --crf, --preset",
	)

	_ = viper.BindPFlag("transcode.src", cmd.Flags().Lookup("src"))
	_ = viper.BindPFlag("transcode.ext", cmd.Flags().Lookup("ext"))
	_ = viper.BindPFlag("transcode.codec", cmd.Flags().Lookup("codec"))
	_ = viper.BindPFlag("transcode.crf", cmd.Flags().Lookup("crf"))
	_ = viper.BindPFlag("transcode.preset", cmd.Flags().Lookup("preset"))
	_ = viper.BindPFlag("smelt.workers", cmd.Flags().Lookup("workers"))
	_ = viper.BindPFlag("transcode.inplace", cmd.Flags().Lookup("inplace"))
	_ = viper.BindPFlag("transcode.output_dir", cmd.Flags().Lookup("output-dir"))
	_ = viper.BindPFlag("transcode.profile", cmd.Flags().Lookup("profile"))
}
