// Package workflow renders a smelt run as a self-contained, human-editable shell
// script (optionally cron-scheduled). There is no separate workflow engine: the
// emitted script — a plain `smelt transcode` invocation with an overlap guard —
// IS the workflow. It can be committed, edited, and scheduled like any script.
package workflow

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Raina-Hardik/smelt/internal/config"
)

// Options are the workflow-specific settings (everything else comes from cfg).
type Options struct {
	Name     string    // workflow name; used in the header and lock file
	Binary   string    // path to the smelt binary the script invokes
	Schedule string    // cron expression, for the documented schedule (optional)
	Version  string    // smelt version, recorded in the header
	Now      time.Time // generation timestamp
}

// Script renders the workflow shell script for cfg + opts.
func Script(cfg *config.Config, opts Options) string {
	name := opts.Name
	if name == "" {
		name = "smelt-workflow"
	}
	bin := opts.Binary
	if bin == "" {
		bin = "smelt"
	}

	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	fmt.Fprintf(&b, "# smelt workflow: %s\n", name)
	fmt.Fprintf(&b, "# generated %s by smelt %s\n", opts.Now.Format(time.RFC3339), versionOr(opts.Version))
	if opts.Schedule != "" {
		fmt.Fprintf(&b, "# schedule: %s\n", opts.Schedule)
	}
	b.WriteString("# This is a plain script — edit it freely.\n")
	b.WriteString("set -eu\n\n")

	// Overlap guard: skip this run if a previous one is still going (cron-safe).
	fmt.Fprintf(&b, "LOCK=\"${TMPDIR:-/tmp}/smelt-%s.lock\"\n", sanitize(name))
	b.WriteString("if command -v flock >/dev/null 2>&1; then\n")
	b.WriteString("\texec 9>\"$LOCK\"\n")
	fmt.Fprintf(&b, "\tflock -n 9 || { echo \"smelt workflow %s: already running\" >&2; exit 0; }\n", name)
	b.WriteString("fi\n\n")

	b.WriteString(shellQuote(bin) + " transcode \\\n")
	args := transcodeArgs(cfg)
	for i, a := range args {
		cont := " \\"
		if i == len(args)-1 {
			cont = ""
		}
		fmt.Fprintf(&b, "\t%s%s\n", a, cont)
	}
	return b.String()
}

// CrontabLine returns a ready-to-paste crontab entry running scriptPath on
// schedule, appending output to a sibling log file.
func CrontabLine(scriptPath, schedule string) string {
	return fmt.Sprintf("%s %s >> %s.log 2>&1", schedule, shellQuote(scriptPath), scriptPath)
}

// transcodeArgs renders the resolved config as `smelt transcode` flags, fully
// expanded so the script doesn't depend on a config file or profile at run time.
func transcodeArgs(cfg *config.Config) []string {
	args := []string{
		flag("--src", cfg.Src),
		flag("--ext", strings.Join(cfg.Ext, ",")),
		flag("--codec", cfg.Codec),
		flag("--crf", strconv.Itoa(cfg.CRF)),
		flag("--preset", cfg.Preset),
		flag("--hwaccel", cfg.HWAccel),
		flag("--workers", strconv.Itoa(cfg.Workers)),
		flag("--suffix", cfg.Suffix),
		flag("--audio-codec", cfg.AudioCodec),
	}
	if cfg.AudioBitrate != "" {
		args = append(args, flag("--audio-bitrate", cfg.AudioBitrate))
	}
	if cfg.SubtitleMode != "" && cfg.SubtitleMode != "copy" {
		args = append(args, flag("--subs", cfg.SubtitleMode))
	}
	if cfg.Container != "" {
		args = append(args, flag("--to", cfg.Container))
	}
	if cfg.OutputDir != "" {
		args = append(args, flag("--output-dir", cfg.OutputDir))
	}
	if cfg.InPlace {
		args = append(args, "--inplace")
	}
	if cfg.SkipHardlinked {
		args = append(args, "--skip-hardlinked")
	}
	for _, c := range cfg.SkipSourceCodecs {
		args = append(args, flag("--skip-source-codec", c))
	}
	if cfg.Force {
		args = append(args, "--force")
	}
	for _, e := range cfg.ExtraArgs {
		args = append(args, flag("--ffmpeg-arg", e))
	}
	// Non-interactive: a scheduled run must never block on a confirmation prompt.
	if cfg.InPlace {
		args = append(args, "--assume-yes")
	}
	return args
}

func flag(name, value string) string { return name + " " + shellQuote(value) }

// shellQuote single-quotes s for POSIX sh, escaping embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sanitize keeps a name safe for a filename (lock file).
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, s)
}

func versionOr(v string) string {
	if v == "" {
		return "(dev)"
	}
	return v
}
