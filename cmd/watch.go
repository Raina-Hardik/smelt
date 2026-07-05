package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/db"
	"github.com/Raina-Hardik/smelt/internal/ffmpeg"
	"github.com/Raina-Hardik/smelt/internal/scanner"
	"github.com/Raina-Hardik/smelt/internal/worker"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Continuously scan a directory and transcode new or changed files",
	Long: `Poll a source directory on a timer, transcoding files that are new or not
yet up to date since the last pass. Accepts every 'smelt transcode' flag to
configure the job; each poll is otherwise an ordinary transcode run — same
scan, same skip/plan logic (so already-transcoded files are left alone),
same worker pool. Runs until interrupted (Ctrl-C / SIGTERM).

With --once, runs a single pass and exits instead of looping — useful to
dry-run the configuration before leaving it running unattended.`,
	Example: `  smelt watch --src /mnt/media --ext mkv,mp4 --codec h265 --interval 5m
  smelt watch --src /mnt/media --profile web --interval 30m
  smelt watch --src /mnt/media --once --dry-run
  smelt watch --src /mnt/media --inplace --interval 1h -y`,
	RunE: runWatch,
}

func init() {
	addTranscodeFlags(watchCmd)
	watchCmd.Flags().Duration(
		"interval", 5*time.Minute,
		"how often to rescan --src for new or changed files",
	)
	watchCmd.Flags().Bool(
		"once", false,
		"run a single scan-and-transcode pass and exit, instead of looping",
	)
	watchCmd.Flags().Bool(
		"dry-run", false,
		"print each pass's transcode plan without executing anything",
	)
	watchCmd.Flags().String(
		"name", "watch",
		"name recorded against each pass's run in the history DB",
	)
	watchCmd.PreRunE = bindTranscodeFlags
	rootCmd.AddCommand(watchCmd)
}

func runWatch(cmd *cobra.Command, _ []string) error {
	if err := ffmpeg.CheckDeps(); err != nil {
		return err
	}
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return exitErr(2, err)
	}
	configureLogger(cfg.LogLevel, cfg.LogFormat)

	interval, _ := cmd.Flags().GetDuration("interval")
	once, _ := cmd.Flags().GetBool("once")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	name, _ := cmd.Flags().GetString("name")
	if !once && interval <= 0 {
		return exitErr(2, fmt.Errorf("--interval must be > 0 (got %s); use --once for a single pass", interval))
	}

	database, err := openDB()
	if err != nil {
		return fmt.Errorf("open history db: %w", err)
	}
	if database != nil {
		defer func() { _ = database.Close() }()
	}

	// Resolve the best hardware-available codec once, up front: it does not
	// change between passes and re-probing every tick would spawn ffmpeg for
	// no reason.
	cfg.Codec = ffmpeg.ResolveCodec(cmd.Context(), cfg.Codec, cfg.HWAccel)
	pool := worker.New(cfg, database)

	// --inplace is destructive; confirm once for the life of the watch rather
	// than once per pass, since a long-running daemon may run thousands of
	// passes unattended.
	if cfg.InPlace && !cfg.AssumeYes && !dryRun {
		ok, err := promptYesNo(os.Stdin, cmd.ErrOrStderr(),
			"--inplace will permanently replace original files as they are discovered by this watch. Continue? [y/N] ")
		if err != nil {
			return err
		}
		if !ok {
			log.Info().Msg("aborted")
			return nil
		}
	}

	log.Info().Str("src", cfg.Src).Strs("ext", cfg.Ext).Dur("interval", interval).Bool("once", once).Msg("watch started")

	ctx := cmd.Context()
	for {
		err := watchPass(ctx, cfg, pool, database, name, dryRun)
		if err != nil {
			log.Error().Err(err).Msg("pass failed")
		}
		if once {
			// A single pass behaves like `smelt transcode`: a failure is
			// reported via the exit code, not swallowed, so scripts/cron
			// invocations can detect it. A long-running loop, below, does
			// not exit over one bad pass — it logs and keeps polling.
			if err != nil {
				return exitErr(3, err)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			log.Info().Msg("watch stopped")
			return nil
		case <-time.After(interval):
		}
	}
}

// watchPass runs one scan-plan-transcode cycle. Files already up to date
// (per worker.Plan's DB/ffprobe smart-skip) are left untouched, so repeated
// passes only ever act on files that are new or have changed since the last
// successful transcode.
func watchPass(ctx context.Context, cfg *config.Config, pool *worker.Pool, database *db.DB, name string, dryRun bool) error {
	files, err := scanner.Scan(cfg.Src, cfg.Ext)
	if err != nil {
		return fmt.Errorf("scan %s: %w", cfg.Src, err)
	}

	todo, skipped, blocked := worker.Plan(ctx, files, cfg, database)
	if blocked > 0 {
		log.Warn().Int("blocked", blocked).Msg("blocked Dolby Vision source(s); re-run with --i-know-this-drops-hdr to transcode them anyway")
	}
	if len(todo) == 0 {
		log.Debug().Int("scanned", len(files)).Int("skipped", skipped).Msg("pass: nothing new")
		return nil
	}

	if dryRun {
		enc, backend := ffmpeg.ResolveEncoder(ctx, cfg.Codec, cfg.HWAccel)
		worker.LogResourceProfile(enc, backend, cfg.DecodeThreads)
		for _, f := range todo {
			log.Info().Str("src", f.Path).Str("dst", worker.OutputPath(f, cfg)).Msg("plan")
		}
		log.Info().Int("files", len(todo)).Msg("dry-run pass complete; nothing written")
		return nil
	}

	log.Info().Int("files", len(todo)).Int("skipped", skipped).Msg("pass: transcoding new files")

	if database == nil {
		return pool.Run(ctx, todo)
	}

	runID := uuid.New().String()
	paths := make([]string, len(todo))
	for i, f := range todo {
		paths[i] = f.Path
	}
	if err := database.StartRun(runID, name, len(todo)); err != nil {
		log.Warn().Err(err).Msg("db: could not register run")
		return pool.Run(ctx, todo)
	}
	if err := database.AddJobs(runID, paths); err != nil {
		log.Warn().Err(err).Msg("db: could not register jobs")
	}

	var ok, failed int
	pool.RunTracked(ctx, todo, runID, nil, func(f scanner.MediaFile, err error) {
		if err != nil {
			failed++
			log.Error().Err(err).Str("file", f.Path).Msg("failed")
		} else {
			ok++
			log.Info().Str("file", f.Path).Msg("done")
		}
	})
	_ = database.FinishRun(runID, ok, failed, skipped)
	if failed > 0 {
		return fmt.Errorf("%d file(s) failed to transcode", failed)
	}
	return nil
}
