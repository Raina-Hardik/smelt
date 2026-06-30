package cmd

import (
	"fmt"

	"github.com/Raina-Hardik/smelt/internal/scanner"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var eachCmd = &cobra.Command{
	Use:   "each",
	Short: "Emit matching file paths one per line",
	Long: `Scan a source directory and emit one resolved file path per line for all
matching files, in the order they would be processed. Intended for use
inside generated program scripts:

    smelt each --src /mnt/media --ext mkv,mp4 | while IFS= read -r f; do
        if smelt match "$f" --codec-ne hevc; then
            smelt do "$f" transcode --codec h265 --crf 23 -y
        fi
    done

With --run-id, registers the run and its file list in the history DB (one
queued job row per file) so the dashboard can track the job from the moment
it starts, before any file is processed. --name labels the run row.`,
	Example: `  smelt each --src /mnt/media --ext mkv,mp4
  smelt each --src /mnt/media --ext mkv --name nightly --run-id "$RUN_ID"`,
	RunE: runEach,
}

func init() {
	eachCmd.Flags().String("src", ".", "source directory to scan")
	eachCmd.Flags().StringSlice("ext", []string{"mkv", "mp4", "avi"}, "file extensions to match")
	eachCmd.Flags().String("name", "", "run name recorded in the history DB (with --run-id)")
	rootCmd.AddCommand(eachCmd)
}

func runEach(cmd *cobra.Command, _ []string) error {
	src, _ := cmd.Flags().GetString("src")
	ext, _ := cmd.Flags().GetStringSlice("ext")
	name, _ := cmd.Flags().GetString("name")
	runID := viper.GetString("smelt.run_id")

	files, err := scanner.Scan(src, ext)
	if err != nil {
		return fmt.Errorf("scan %s: %w", src, err)
	}

	// Register the run in the DB before emitting any path, so the first
	// downstream `smelt do` finds its job row already present.
	if runID != "" {
		if database, err := openDB(); err != nil {
			log.Warn().Err(err).Msg("db: could not open history db")
		} else if database != nil {
			defer func() { _ = database.Close() }()
			paths := make([]string, len(files))
			for i, f := range files {
				paths[i] = f.Path
			}
			if err := database.StartRun(runID, name, len(files)); err != nil {
				log.Warn().Err(err).Msg("db: could not register run")
			} else if err := database.AddJobs(runID, paths); err != nil {
				log.Warn().Err(err).Msg("db: could not register jobs")
			}
		}
	}

	out := cmd.OutOrStdout()
	for _, f := range files {
		if _, err := fmt.Fprintln(out, f.Path); err != nil {
			return err
		}
	}
	return nil
}
