package cmd

import (
	"fmt"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/scanner"
	"github.com/Raina-Hardik/smelt/internal/worker"
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
	transcodeCmd.PreRunE = bindTranscodeFlags

	rootCmd.AddCommand(transcodeCmd)
}

func runTranscode(cmd *cobra.Command, args []string) error {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return err
	}
	configureLogger(cfg.LogLevel, cfg.LogFormat)

	files, err := scanner.Scan(cfg.Src, cfg.Ext)
	if err != nil {
		return fmt.Errorf("scan %s: %w", cfg.Src, err)
	}
	if len(files) == 0 {
		log.Warn().Str("src", cfg.Src).Strs("ext", cfg.Ext).Msg("no matching files")
		return nil
	}

	files, skipped := worker.Plan(cmd.Context(), files, cfg)
	if skipped > 0 {
		log.Info().Int("skipped", skipped).Msg("skipped up-to-date files (use --force to re-transcode)")
	}
	if len(files) == 0 {
		log.Info().Msg("nothing to transcode; all outputs already exist")
		return nil
	}

	if ok, err := confirmInplace(cfg, len(files)); err != nil {
		return err
	} else if !ok {
		log.Info().Msg("aborted")
		return nil
	}

	if cfg.DryRun {
		for _, f := range files {
			log.Info().
				Str("src", f.Path).
				Str("dst", worker.OutputPath(f, cfg)).
				Str("codec", cfg.Codec).
				Int("crf", cfg.CRF).
				Msg("plan")
		}
		log.Info().Int("files", len(files)).Msg("dry-run complete; nothing written")
		return nil
	}

	return worker.New(cfg).Run(cmd.Context(), files)
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
		"encoding preset; normalized into the chosen encoder's namespace (e.g. x264 'superfast' → nvenc 'fast')",
	)
	cmd.Flags().String(
		"hwaccel", "auto",
		"hardware acceleration: auto|none|nvenc|qsv|vaapi|amf|videotoolbox",
	)
	cmd.Flags().Int(
		"workers", 0,
		"maximum parallel transcode jobs; 0 = runtime.NumCPU()",
	)
	cmd.Flags().Bool(
		"inplace", false,
		"replace original after transcode; files already in the target codec are skipped (use --force to re-encode)",
	)
	cmd.Flags().Bool(
		"force", false,
		"re-transcode even if the output file already exists",
	)
	cmd.Flags().String(
		"output-dir", "",
		"write output files to this directory instead of alongside source",
	)
	cmd.Flags().String(
		"suffix", ".smelt",
		"filename suffix for outputs written alongside the source",
	)
	cmd.Flags().String(
		"to", "",
		"target container/format for outputs: mp4|mkv|webm|... (default: keep source container)",
	)
	cmd.Flags().String(
		"profile", "",
		"named profile preset from config; explicit flags still override it",
	)
	cmd.Flags().StringArray(
		"ffmpeg-arg", nil,
		"raw ffmpeg argument passed through verbatim; repeatable (e.g. --ffmpeg-arg=-vf --ffmpeg-arg=scale=1280:-2)",
	)
}

// bindTranscodeFlags binds the invoked command's flags to viper at run time.
// It must run in PreRunE (not init) because transcode and tui share the same
// flag set: binding at init would let whichever command registers last win the
// global viper key, so a flag set on the other command would be silently ignored.
func bindTranscodeFlags(cmd *cobra.Command, _ []string) error {
	binds := []struct{ key, flag string }{
		{"transcode.src", "src"},
		{"transcode.ext", "ext"},
		{"transcode.codec", "codec"},
		{"transcode.crf", "crf"},
		{"transcode.preset", "preset"},
		{"transcode.hwaccel", "hwaccel"},
		{"smelt.workers", "workers"},
		{"transcode.inplace", "inplace"},
		{"transcode.force", "force"},
		{"transcode.output_dir", "output-dir"},
		{"transcode.suffix", "suffix"},
		{"transcode.to", "to"},
		{"transcode.profile", "profile"},
		{"transcode.ffmpeg_args", "ffmpeg-arg"},
		{"transcode.dry_run", "dry-run"}, // transcode only; skipped where absent
	}
	for _, b := range binds {
		if f := cmd.Flags().Lookup(b.flag); f != nil {
			if err := viper.BindPFlag(b.key, f); err != nil {
				return err
			}
		}
	}
	return nil
}
