package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/scanner"
	"github.com/Raina-Hardik/smelt/internal/worker"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var doCmd = &cobra.Command{
	Use:   "do <file> <subcommand>",
	Short: "Apply a smelt subcommand to a single file",
	Long: `Apply a smelt subcommand to a single file rather than scanning a directory.
Typically called from a generated program script body:

    smelt each --src /mnt/media | while IFS= read -r f; do
        if smelt match "$f" --codec-ne hevc; then
            smelt do "$f" transcode --codec h265 --crf 23 -y
        fi
    done

Subcommands:
  transcode   transcode the file with the given encode flags
  check       run a health check on the file (exits 1 if corrupt)`,
	Example: `  smelt do movie.mkv transcode --codec h265 --crf 23 -y
  smelt do movie.mkv check`,
	Args:         cobra.MinimumNArgs(2),
	SilenceUsage: true,
	RunE:         runDo,
}

func init() {
	addTranscodeFlags(doCmd)
	// src, ext, and workers are directory-scanning flags that don't apply to a
	// single-file command; hide them so the help screen isn't misleading.
	_ = doCmd.Flags().MarkHidden("src")
	_ = doCmd.Flags().MarkHidden("ext")
	_ = doCmd.Flags().MarkHidden("workers")
	doCmd.PreRunE = bindTranscodeFlags
	rootCmd.AddCommand(doCmd)
}

func runDo(cmd *cobra.Command, args []string) error {
	path := args[0]
	sub := args[1]

	switch sub {
	case "transcode":
		return doTranscode(cmd, path)
	case "check":
		return doCheck(cmd, path)
	default:
		return exitErr(2, fmt.Errorf("unknown subcommand %q; valid: transcode, check", sub))
	}
}

func doTranscode(cmd *cobra.Command, path string) error {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return exitErr(2, err)
	}
	configureLogger(cfg.LogLevel, cfg.LogFormat)

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	f := scanner.MediaFile{
		Path:    path,
		RelPath: filepath.Base(path),
		Ext:     filepath.Ext(path),
		Size:    info.Size(),
		Mtime:   info.ModTime().Unix(),
		Mode:    info.Mode(),
	}

	database, err := openDB()
	if err != nil {
		return fmt.Errorf("open history db: %w", err)
	}
	if database != nil {
		defer func() { _ = database.Close() }()
	}

	runID := cfg.RunID
	pool := worker.New(cfg, database)
	if runID != "" && database != nil {
		pool.RunTracked(cmd.Context(), []scanner.MediaFile{f}, runID,
			nil,
			func(mf scanner.MediaFile, e error) {
				if e != nil {
					log.Error().Err(e).Str("file", mf.Path).Msg("failed")
				} else {
					log.Info().Str("file", mf.Path).Msg("done")
				}
			},
		)
		return nil
	}

	if err := pool.Run(cmd.Context(), []scanner.MediaFile{f}); err != nil {
		return exitErr(3, err)
	}
	return nil
}

func doCheck(cmd *cobra.Command, path string) error {
	return checkOneFile(cmd.Context(), path)
}
