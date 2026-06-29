package cmd

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/Raina-Hardik/smelt/internal/ffmpeg"
	"github.com/Raina-Hardik/smelt/internal/scanner"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/sync/semaphore"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Probe a directory for corrupt or unreadable media files",
	Long: `Scan a source directory and probe each matching file with ffprobe.

Reports files that cannot be opened (corrupt container, read error) and files
with no video stream. Exits 0 if all files are healthy, 1 if any are corrupt.`,
	Example: `  smelt check --src /mnt/media
  smelt check --src /mnt/media --ext mkv,mp4 --workers 4`,
	RunE: runCheck,
}

func init() {
	checkCmd.Flags().String(
		"src", ".",
		"source directory to scan",
	)
	checkCmd.Flags().StringSlice(
		"ext", []string{"mkv", "mp4", "avi"},
		"file extensions to probe",
	)
	checkCmd.Flags().Int(
		"workers", 0,
		"parallel probe jobs; 0 = runtime.NumCPU()",
	)
	checkCmd.PreRunE = bindCheckFlags
	rootCmd.AddCommand(checkCmd)
}

func bindCheckFlags(cmd *cobra.Command, _ []string) error {
	for _, b := range []struct{ key, flag string }{
		{"transcode.src", "src"},
		{"transcode.ext", "ext"},
		{"smelt.workers", "workers"},
	} {
		if f := cmd.Flags().Lookup(b.flag); f != nil {
			if err := viper.BindPFlag(b.key, f); err != nil {
				return err
			}
		}
	}
	return nil
}

func runCheck(cmd *cobra.Command, _ []string) error {
	configureLogger(viper.GetString("smelt.log_level"), viper.GetString("smelt.log_format"))

	src := viper.GetString("transcode.src")
	ext := viper.GetStringSlice("transcode.ext")
	workers := viper.GetInt("smelt.workers")
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	files, err := scanner.Scan(src, ext)
	if err != nil {
		return fmt.Errorf("scan %s: %w", src, err)
	}
	if len(files) == 0 {
		log.Warn().Str("src", src).Strs("ext", ext).Msg("no matching files")
		return nil
	}
	log.Info().Int("files", len(files)).Str("src", src).Msg("probing")

	sem := semaphore.NewWeighted(int64(workers))
	var wg sync.WaitGroup
	var corrupt atomic.Int64

	for _, f := range files {
		if err := sem.Acquire(cmd.Context(), 1); err != nil {
			break // context cancelled
		}
		f := f
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer sem.Release(1)

			info, err := ffmpeg.ProbeHealth(cmd.Context(), f.Path)
			if err != nil {
				corrupt.Add(1)
				log.Error().Err(err).Str("file", f.Path).Msg("corrupt")
				return
			}
			if !info.HasVideo {
				log.Warn().Str("file", f.Path).Msg("no video stream")
			}
		}()
	}
	wg.Wait()

	n := int(corrupt.Load())
	log.Info().Int("files", len(files)).Int("corrupt", n).Msg("check complete")
	if n > 0 {
		return fmt.Errorf("%d corrupt file(s)", n)
	}
	return nil
}
