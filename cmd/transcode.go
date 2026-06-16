package cmd

import (
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/scanner"
	"github.com/Raina-Hardik/smelt/internal/worker"
)

var transcodeCmd = &cobra.Command{
	Use:   "transcode",
	Short: "Transcode media files in a directory",
	Example: `  smelt transcode --src /mnt/media --ext mkv,mp4 --codec h265 --workers 4
  smelt transcode --src /mnt/media --dry-run`,
	RunE: runTranscode,
}

func init() {
	transcodeCmd.Flags().Bool("dry-run", false, "print the transcode plan without executing")
	_ = viper.BindPFlag("dry_run", transcodeCmd.Flags().Lookup("dry-run"))

	rootCmd.AddCommand(transcodeCmd)
}

func runTranscode(cmd *cobra.Command, args []string) error {
	cfg := config.Load()

	files, err := scanner.Scan(cfg.Src, cfg.Extensions)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	log.Info().
		Str("src", cfg.Src).
		Strs("ext", cfg.Extensions).
		Int("files", len(files)).
		Str("codec", cfg.Codec).
		Int("workers", cfg.Workers).
		Bool("inplace", cfg.InPlace).
		Bool("dry_run", cfg.DryRun).
		Msg("transcode plan")

	if cfg.DryRun {
		for _, f := range files {
			fmt.Printf("[dry-run]  %s  →  %s  (codec: %s)\n",
				f.Path, worker.OutputPath(f.Path), cfg.Codec)
		}
		return nil
	}

	if len(files) == 0 {
		log.Info().Msg("no files matched — nothing to do")
		return nil
	}

	pool := worker.New(cfg)
	return pool.Run(cmd.Context(), files)
}
