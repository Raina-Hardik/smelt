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
	Long: `Generate a self-contained, human-editable shell script that runs smelt.

Two modes:

  Rule mode (--rule): renders a per-file decision pipeline. Each --rule is
  evaluated in order; the first matching rule wins. Rules follow the manifest
  syntax:

    [when <field> <op> <value> [and ...]] do <action> [flags]

  Fields: codec, audio, height, width, bitrate, duration, ext
  Ops:    eq, ne, gt, lt, ge, le
  Actions: transcode [flags] | check | skip

  Simple mode (no --rule): renders a single 'smelt transcode' invocation.
  Accepts every 'smelt transcode' flag to configure the job.

In both modes the script includes a flock overlap guard (cron-safe), an
optional gum banner, and run-ID tracking for the history dashboard. With
--out the script is written to a file and made executable; otherwise it is
printed to stdout. With --schedule a ready-to-paste crontab line is printed
(requires --out).`,
	Example: `  # Rule mode — per-file decision pipeline
  smelt workflow --src /mnt/media \
      --rule "when codec ne hevc and height gt 1080 do transcode --codec h265 --crf 24" \
      --rule "when codec ne hevc do transcode --codec h265 --crf 23" \
      --name nightly --schedule "0 3 * * *" -o nightly.sh

  # Simple mode — single transcode invocation
  smelt workflow --src /mnt/media --codec h265 -o nightly.sh
  smelt workflow --src /mnt/media --inplace -o nightly.sh --schedule "0 3 * * *"`,
	RunE: runWorkflow,
}

func init() {
	addTranscodeFlags(workflowCmd)
	workflowCmd.Flags().StringP("out", "o", "", "write the script to this file (made executable); default stdout")
	workflowCmd.Flags().String("name", "", "workflow name, used in the script header and lock file")
	workflowCmd.Flags().String("schedule", "", `cron expression to run the script on a timer, e.g. "0 3 * * *" (requires --out)`)
	workflowCmd.Flags().StringArray("rule", nil, `rule in manifest syntax: "[when <field> <op> <value> [and ...]]] do <action> [flags]"; repeatable, first match wins`)
	workflowCmd.PreRunE = bindTranscodeFlags
	rootCmd.AddCommand(workflowCmd)
}

func runWorkflow(cmd *cobra.Command, _ []string) error {
	cfg := config.Load()

	out, _ := cmd.Flags().GetString("out")
	name, _ := cmd.Flags().GetString("name")
	schedule, _ := cmd.Flags().GetString("schedule")
	ruleStrs, _ := cmd.Flags().GetStringArray("rule")

	if schedule != "" && out == "" {
		return fmt.Errorf("--schedule requires --out (cron needs a script path to run)")
	}

	bin, err := os.Executable()
	if err != nil {
		bin = "smelt"
	}
	opts := workflow.Options{
		Name:     name,
		Binary:   bin,
		Schedule: schedule,
		Version:  version,
		Now:      time.Now(),
		DBSet:    true,
		DBPath:   cfg.DBPath,
	}

	var script string
	if len(ruleStrs) > 0 {
		rules := make([]workflow.Rule, 0, len(ruleStrs))
		for _, rs := range ruleStrs {
			r, err := workflow.ParseRule(rs)
			if err != nil {
				return exitErr(2, fmt.Errorf("invalid --rule %q: %w", rs, err))
			}
			rules = append(rules, r)
		}
		p := workflow.Program{
			Name:     name,
			Schedule: schedule,
			Src:      cfg.Src,
			Ext:      cfg.Ext,
			Rules:    rules,
		}
		script = workflow.Render(p, opts)
	} else {
		if err := cfg.Validate(); err != nil {
			return err
		}
		script = workflow.Script(cfg, opts)
	}

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
