package cmd

import (
	"fmt"
	"sync/atomic"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/ffmpeg"
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
	if err := ffmpeg.CheckDeps(); err != nil {
		return err
	}
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return exitErr(2, err)
	}
	configureLogger(cfg.LogLevel, cfg.LogFormat)

	database, err := openDB()
	if err != nil {
		return fmt.Errorf("open history db: %w", err)
	}
	if database != nil {
		defer func() { _ = database.Close() }()
	}

	files, err := scanner.Scan(cfg.Src, cfg.Ext)
	if err != nil {
		return fmt.Errorf("scan %s: %w", cfg.Src, err)
	}
	if len(files) == 0 {
		log.Warn().Str("src", cfg.Src).Strs("ext", cfg.Ext).Msg("no matching files")
		return nil
	}

	// Resolve the best hardware-available codec before planning so that the
	// smart-skip target matches what will actually be encoded.
	cfg.Codec = ffmpeg.ResolveCodec(cmd.Context(), cfg.Codec, cfg.HWAccel)

	files, skipped, blocked := worker.Plan(cmd.Context(), files, cfg, database)
	if skipped > 0 {
		log.Info().Int("skipped", skipped).Msg("skipped up-to-date files (use --force to re-transcode)")
	}
	if blocked > 0 {
		log.Warn().Int("blocked", blocked).Msg("blocked Dolby Vision source(s); re-run with --i-know-this-drops-hdr to transcode them anyway")
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
		enc, backend := ffmpeg.ResolveEncoder(cmd.Context(), cfg.Codec, cfg.HWAccel)
		worker.LogResourceProfile(enc, backend, cfg.DecodeThreads, cfg.HWDecode)
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

	runID := cfg.RunID
	if runID != "" && database != nil {
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.Path
		}
		if err := database.StartRun(runID, "", len(files)); err != nil {
			log.Warn().Err(err).Msg("db: could not register run")
			runID = ""
		} else if err := database.AddJobs(runID, paths); err != nil {
			log.Warn().Err(err).Msg("db: could not register jobs")
		}
	}

	pool := worker.New(cfg, database)
	if runID != "" && database != nil {
		var ok, failed atomic.Int64
		pool.RunTracked(cmd.Context(), files, runID,
			nil,
			func(f scanner.MediaFile, err error) {
				if err != nil {
					failed.Add(1)
					log.Error().Err(err).Str("file", f.Path).Msg("failed")
				} else {
					ok.Add(1)
					log.Info().Str("file", f.Path).Msg("done")
				}
			},
		)
		okN, failedN := int(ok.Load()), int(failed.Load())
		_ = database.FinishRun(runID, okN, failedN, skipped)
		if failedN == len(files) {
			return exitErr(4, fmt.Errorf("%d file(s) failed to transcode", failedN))
		}
		if failedN > 0 {
			return exitErr(3, fmt.Errorf("%d file(s) failed to transcode", failedN))
		}
		return nil
	}

	if err := pool.Run(cmd.Context(), files); err != nil {
		return exitErr(3, err)
	}
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
		"encoding preset; normalized into the chosen encoder's namespace (e.g. x264 'superfast' → nvenc 'fast')",
	)
	cmd.Flags().String(
		"hwaccel", "auto",
		"hardware acceleration: auto|none|nvenc|qsv|vaapi|amf|videotoolbox",
	)
	cmd.Flags().String(
		"hwdecode", "auto",
		"hardware decode: auto|off; auto decodes on the hardware encoder's device when a per-file probe confirms the source is decodable there, else software (a hardware-decode failure retries that file once with software decode); off forces software decode",
	)
	cmd.Flags().String(
		"audio-codec", "copy",
		"audio codec: copy|aac|opus|mp3|ac3|flac (copy = passthrough, no re-encode)",
	)
	cmd.Flags().String(
		"audio-bitrate", "",
		"audio bitrate when re-encoding, e.g. 192k (ignored when --audio-codec=copy)",
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
		"skip-hardlinked", false,
		"with --inplace, skip files that are hardlinked elsewhere (transcoding would break the link and double disk usage)",
	)
	cmd.Flags().StringArray(
		"skip-source-codec", nil,
		"skip files whose current video codec matches; repeatable (e.g. --skip-source-codec av1 to never downgrade AV1 files)",
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
		"named profile: a preconfigured flag set (codec, crf, preset, audio, container, extra ffmpeg args). Built-ins: web, web-hevc, archive, av1, mobile; config.yaml profiles override same-named built-ins. Explicit flags still win. See `smelt profiles`",
	)
	_ = cmd.RegisterFlagCompletionFunc("profile",
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return config.ProfileNames(), cobra.ShellCompDirectiveNoFileComp
		})
	cmd.Flags().StringArray(
		"ffmpeg-arg", nil,
		"raw ffmpeg argument passed through verbatim; repeatable (e.g. --ffmpeg-arg=-vf --ffmpeg-arg=scale=1280:-2)",
	)
	cmd.Flags().String(
		"subs", "copy",
		"subtitle stream handling: copy (preserve all subtitle tracks) | drop (strip all subtitles)",
	)
	cmd.Flags().Int(
		"decode-threads", 0,
		"cap ffmpeg's decoder thread count (0 = ffmpeg default); unlike --ffmpeg-arg this lands before -i, so it constrains the decode side, not just the encoder; omitted while hardware decode is active, still applied on every software-decode path",
	)
	cmd.Flags().Bool(
		"i-know-this-drops-hdr", false,
		"required to transcode a source with a detected Dolby Vision stream; smelt drops the DV RPU layer on re-encode with no passthrough support yet, so this flag is an explicit acknowledgement rather than a silent quality loss",
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
		{"transcode.hwdecode", "hwdecode"},
		{"transcode.audio_codec", "audio-codec"},
		{"transcode.audio_bitrate", "audio-bitrate"},
		{"smelt.workers", "workers"},
		{"transcode.inplace", "inplace"},
		{"transcode.skip_hardlinked", "skip-hardlinked"},
		{"transcode.skip_source_codecs", "skip-source-codec"},
		{"transcode.force", "force"},
		{"transcode.output_dir", "output-dir"},
		{"transcode.suffix", "suffix"},
		{"transcode.to", "to"},
		{"transcode.profile", "profile"},
		{"transcode.ffmpeg_args", "ffmpeg-arg"},
		{"transcode.subs", "subs"},
		{"transcode.decode_threads", "decode-threads"},
		{"transcode.allow_hdr_loss", "i-know-this-drops-hdr"},
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
