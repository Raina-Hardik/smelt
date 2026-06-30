package cmd

import (
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var finishRunCmd = &cobra.Command{
	Use:   "finish-run",
	Short: "Reconcile and close a tracked run in the history database",
	Long: `Close a tracked run in the history database. Jobs still 'queued' (no rule
matched them) become 'skipped'; jobs left 'running' (a worker died mid-encode)
become 'failed'. The run's ok/failed/skipped counters and final status are then
derived from the reconciled job rows.

Emitted automatically at the end of a generated program script; rarely run by
hand. Requires --run-id.`,
	Example: `  smelt finish-run --run-id "$RUN_ID"`,
	RunE:    runFinishRun,
}

func init() {
	rootCmd.AddCommand(finishRunCmd)
}

func runFinishRun(cmd *cobra.Command, _ []string) error {
	runID := viper.GetString("smelt.run_id")
	if runID == "" {
		return exitErr(2, fmt.Errorf("finish-run requires --run-id"))
	}

	database, err := openDB()
	if err != nil {
		return fmt.Errorf("open history db: %w", err)
	}
	if database == nil {
		log.Warn().Msg("history DB disabled; nothing to finish")
		return nil
	}
	defer func() { _ = database.Close() }()

	if err := database.ReconcileAndFinishRun(runID); err != nil {
		return fmt.Errorf("finish run %s: %w", runID, err)
	}
	log.Info().Str("run_id", runID).Msg("run closed")
	return nil
}
