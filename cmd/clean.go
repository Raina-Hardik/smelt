package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

const transientStem = ".transcoded"

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove leftover .transcoded partial artifacts from interrupted runs",
	Long: `Remove leftover .transcoded partial artifacts from interrupted transcode runs.

When smelt is killed mid-encode (SIGKILL, power loss, OOM) the transient
working file (<name>.transcoded<ext>) is not cleaned up. This command scans
--src for those files and deletes them. Use --dry-run first to preview.`,
	Example: `  smelt clean --src /mnt/media --dry-run
  smelt clean --src /mnt/media
  smelt clean --src /mnt/media --ext mkv`,
	RunE: runClean,
}

func init() {
	cleanCmd.Flags().String(
		"src", ".",
		"source directory to scan",
	)
	cleanCmd.Flags().StringSlice(
		"ext", []string{"mkv", "mp4", "avi"},
		"file extensions to scan",
	)
	cleanCmd.Flags().Bool(
		"dry-run", false,
		"print files that would be removed without deleting anything",
	)
	rootCmd.AddCommand(cleanCmd)
}

func runClean(cmd *cobra.Command, _ []string) error {
	src, _ := cmd.Flags().GetString("src")
	exts, _ := cmd.Flags().GetStringSlice("ext")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	extSet := make(map[string]struct{}, len(exts))
	for _, e := range exts {
		extSet["."+strings.ToLower(strings.TrimPrefix(e, "."))] = struct{}{}
	}

	var found []string
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == src {
				return err
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if _, ok := extSet[ext]; !ok {
			return nil
		}
		stem := strings.TrimSuffix(filepath.Base(path), ext)
		if strings.HasSuffix(stem, transientStem) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan %s: %w", src, err)
	}

	if len(found) == 0 {
		log.Info().Str("src", src).Msg("no orphaned artifacts found")
		return nil
	}

	if dryRun {
		for _, p := range found {
			log.Info().Str("file", p).Msg("would remove")
		}
		log.Info().Int("files", len(found)).Msg("dry-run complete; nothing deleted")
		return nil
	}

	assumeYes, _ := cmd.Root().PersistentFlags().GetBool("assume-yes")
	if !assumeYes {
		fmt.Fprintf(os.Stderr, "Found %d artifact(s) to remove. Proceed? [y/N] ", len(found))
		var resp string
		if _, err := fmt.Fscan(os.Stdin, &resp); err != nil {
			return fmt.Errorf("reading confirmation: %w", err)
		}
		if strings.ToLower(resp) != "y" {
			log.Info().Msg("aborted")
			return nil
		}
	}

	var failed int
	for _, p := range found {
		if err := os.Remove(p); err != nil {
			log.Error().Err(err).Str("file", p).Msg("failed to remove")
			failed++
			continue
		}
		log.Info().Str("file", p).Msg("removed")
	}
	log.Info().Int("removed", len(found)-failed).Int("failed", failed).Msg("clean complete")
	if failed > 0 {
		return fmt.Errorf("%d file(s) could not be removed", failed)
	}
	return nil
}
