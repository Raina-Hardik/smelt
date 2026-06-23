package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/workflow"
	"github.com/spf13/cobra"
)

var workflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Generate a schedulable shell script for a transcode job",
	Long: `Generate a self-contained, human-editable shell script that runs a smelt
transcode. The script is plain — it IS the workflow; there is no separate engine.
It includes an overlap guard (flock) so scheduled runs never stack.

Accepts every 'smelt transcode' flag to define the job. With --out the script is
written to a file and made executable; otherwise it is printed to stdout. With
--schedule a ready-to-paste crontab line is printed (requires --out).`,
	Example: `  smelt workflow --src /mnt/media --codec h265 -o nightly.sh
  smelt workflow --src /mnt/media --inplace -o nightly.sh --schedule "0 3 * * *"`,
	RunE: runWorkflow,
}

func init() {
	addTranscodeFlags(workflowCmd)
	workflowCmd.Flags().StringP("out", "o", "", "write the script to this file (made executable); default stdout")
	workflowCmd.Flags().String("name", "", "workflow name, used in the script header and lock file")
	workflowCmd.Flags().String("schedule", "", `cron expression to run the script on a timer, e.g. "0 3 * * *" (requires --out)`)
	workflowCmd.PreRunE = bindTranscodeFlags
	rootCmd.AddCommand(workflowCmd)
}

func runWorkflow(cmd *cobra.Command, _ []string) error {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return err
	}

	out, _ := cmd.Flags().GetString("out")
	name, _ := cmd.Flags().GetString("name")
	schedule, _ := cmd.Flags().GetString("schedule")
	if schedule != "" && out == "" {
		return fmt.Errorf("--schedule requires --out (cron needs a script path to run)")
	}

	bin, err := os.Executable()
	if err != nil {
		bin = "smelt"
	}
	script := workflow.Script(cfg, workflow.Options{
		Name:     name,
		Binary:   bin,
		Schedule: schedule,
		Version:  version,
		Now:      time.Now(),
	})

	if out == "" {
		_, err := fmt.Fprint(cmd.OutOrStdout(), script)
		return err
	}
	if err := os.WriteFile(out, []byte(script), 0o755); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s\n", out)
	if schedule != "" {
		abs := out
		if a, err := filepath.Abs(out); err == nil {
			abs = a
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nschedule it by adding this crontab line (crontab -e):\n  %s\n",
			workflow.CrontabLine(abs, schedule))
	}
	return nil
}
