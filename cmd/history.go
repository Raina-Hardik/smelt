package cmd

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "Show recent transcode history from the processed-file database",
	Long: `Query the SQLite history database and print recent transcode records.

Each record shows when the transcode completed, its status, the encoder and
settings used, how long it took, and the source file path. Useful for auditing
what smelt has done and verifying encode settings over time.`,
	Example: `  smelt history
  smelt history --limit 50
  smelt history --failed
  smelt history --db /custom/path/history.db`,
	RunE: runHistory,
}

func init() {
	historyCmd.Flags().Int(
		"limit", 20,
		"number of most recent records to show",
	)
	historyCmd.Flags().Bool(
		"failed", false,
		"show only failed transcodes",
	)
	rootCmd.AddCommand(historyCmd)
}

func runHistory(cmd *cobra.Command, _ []string) error {
	dbPath := viper.GetString("smelt.db")
	if dbPath == "" {
		return fmt.Errorf("history database is disabled (--db is empty)")
	}

	database, err := openDB()
	if err != nil {
		return fmt.Errorf("open history db: %w", err)
	}
	if database == nil {
		return fmt.Errorf("history database is disabled (--db is empty)")
	}
	defer database.Close()

	limit, _ := cmd.Flags().GetInt("limit")
	failedOnly, _ := cmd.Flags().GetBool("failed")

	records, err := database.Recent(limit, failedOnly)
	if err != nil {
		return fmt.Errorf("query history: %w", err)
	}
	if len(records) == 0 {
		fmt.Println("no records found")
		return nil
	}

	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "COMPLETED\tSTATUS\tENCODER\tCRF\tELAPSED\tSOURCE")
	for _, r := range records {
		elapsed := time.Duration(r.ElapsedMs) * time.Millisecond
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n",
			r.CompletedAt.Local().Format("2006-01-02 15:04:05"),
			r.Status,
			encoderLabel(r.Encoder, r.Backend),
			r.CRF,
			fmtElapsed(elapsed),
			r.SourcePath,
		)
	}
	return tw.Flush()
}

func encoderLabel(encoder, backend string) string {
	if encoder == "" {
		return "(unknown)"
	}
	if backend != "" {
		return encoder // e.g. hevc_nvenc
	}
	return encoder // software encoder like libx265
}

func fmtElapsed(d time.Duration) string {
	if d < time.Second {
		return "0s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%ds", m, s)
}
