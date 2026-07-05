package workflow

import (
	"fmt"
	"sort"
	"strings"
)

// Cond is a single file-level predicate.
// Field: codec | audio | ext | height | width | bitrate | duration
// Op:    eq | ne | gt | lt | ge | le
//
//	(codec/audio/ext support eq/ne; numeric fields support all six)
type Cond struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

// Action is a smelt subcommand applied to a matching file.
// Cmd:  "transcode" | "check" | "skip"
// Args are the raw CLI flag tokens for the subcommand (already shell-safe,
// e.g. ["--codec", "'h265'", "--crf", "'24'"]); empty for skip.
// Build Args from a *config.Config via TranscodeArgs.
type Action struct {
	Cmd  string   `json:"cmd"`
	Args []string `json:"args"`
}

// Rule is a condition set → action. All Conds are AND-combined.
// Empty When means "always match" — a catch-all (e.g. a final `do skip`).
type Rule struct {
	When []Cond `json:"when"`
	Do   Action `json:"do"`
}

// Program is the IR for a smelt decision workflow.
type Program struct {
	Name     string
	Schedule string
	Src      string
	Ext      []string
	Rules    []Rule
}

// Render emits a gum-friendly, human-editable shell script that encodes p.
// The shell body is generated from the manifest block; Parse reads only the
// manifest — never arbitrary bash.
func Render(p Program, opts Options) string {
	name := p.Name
	if name == "" {
		name = "smelt-program"
	}
	bin := opts.Binary
	if bin == "" {
		bin = "smelt"
	}
	qbin := shellQuote(bin)

	var b strings.Builder

	b.WriteString("#!/bin/sh\n")
	fmt.Fprintf(&b, "# smelt program: %s\n", name)
	fmt.Fprintf(&b, "# generated %s by smelt %s\n", opts.Now.Format("2006-01-02T15:04:05Z07:00"), versionOr(opts.Version))
	if p.Schedule != "" {
		fmt.Fprintf(&b, "# schedule: %s\n", p.Schedule)
	}
	b.WriteString("# This is a plain script — edit the smelt:manifest block, or the body, freely.\n")

	// Manifest block — the machine-parseable source of truth.
	b.WriteString("\n# >>> smelt:manifest v1 >>>\n")
	fmt.Fprintf(&b, "# name: %s\n", name)
	if p.Schedule != "" {
		fmt.Fprintf(&b, "# schedule: %s\n", p.Schedule)
	}
	fmt.Fprintf(&b, "# src: %s\n", p.Src)
	fmt.Fprintf(&b, "# ext: %s\n", strings.Join(p.Ext, ","))
	for _, r := range p.Rules {
		fmt.Fprintf(&b, "# rule: %s\n", renderRuleLine(r))
	}
	b.WriteString("# <<< smelt:manifest <<<\n\n")

	b.WriteString("set -eu\n\n")

	// Overlap guard.
	fmt.Fprintf(&b, "LOCK=\"${TMPDIR:-/tmp}/smelt-%s.lock\"\n", sanitize(name))
	b.WriteString("if command -v flock >/dev/null 2>&1; then\n")
	b.WriteString("\texec 9>\"$LOCK\"\n")
	fmt.Fprintf(&b, "\tflock -n 9 || { echo \"smelt program %s: already running\" >&2; exit 0; }\n", name)
	b.WriteString("fi\n\n")

	// Optional gum affordance — TTY-gated, never blocks cron.
	b.WriteString("if [ -t 1 ] && command -v gum >/dev/null 2>&1; then\n")
	fmt.Fprintf(&b, "\tgum style --border normal \"smelt: %s\"\n", name)
	b.WriteString("fi\n\n")

	// Run identity: honour a server-injected SMELT_RUN_ID, else generate one so
	// even cron and manual runs are visible in the dashboard.
	b.WriteString("RUN_ID=\"${SMELT_RUN_ID:-$(date +%s)-$$}\"\n\n")

	dbFlag := ""
	if opts.DBSet {
		dbFlag = " --db " + shellQuote(opts.DBPath)
	}

	fmt.Fprintf(&b, "%s each --src %s --ext %s --name %s --run-id \"$RUN_ID\"%s | while IFS= read -r _smelt_file; do\n",
		qbin, shellQuote(p.Src), shellQuote(strings.Join(p.Ext, ",")), shellQuote(name), dbFlag)

	hasBody := false
	hasIf := false
	for _, r := range p.Rules {
		hasBody = true
		if len(r.When) == 0 {
			// Catch-all: first match wins, so nothing after it can fire.
			if hasIf {
				fmt.Fprintf(&b, "\telse\n\t\t%s\n", branchBody(qbin, r.Do, dbFlag))
			} else {
				fmt.Fprintf(&b, "\t%s\n", branchBody(qbin, r.Do, dbFlag))
			}
			break
		}
		kw := "elif"
		if !hasIf {
			kw = "if"
			hasIf = true
		}
		fmt.Fprintf(&b, "\t%s %s match \"$_smelt_file\"%s; then\n\t\t%s\n",
			kw, qbin, renderMatchArgs(r.When), branchBody(qbin, r.Do, dbFlag))
	}
	if hasIf {
		b.WriteString("\tfi\n")
	}
	// An empty while body is a syntax error in POSIX sh; emit a no-op when
	// there are no rules so the script is always syntactically valid.
	if !hasBody {
		b.WriteString("\t:\n")
	}
	b.WriteString("done\n\n")

	fmt.Fprintf(&b, "%s finish-run --run-id \"$RUN_ID\"%s\n", qbin, dbFlag)
	return b.String()
}

// branchBody renders the shell command run when a rule matches. transcode and
// check delegate to `smelt do`; skip (and anything unknown) is a no-op so the
// file is left untouched (finish-run later records it as skipped). A failing
// `do` must not abort the whole program, hence `|| true`.
func branchBody(qbin string, a Action, dbFlag string) string {
	switch a.Cmd {
	case "transcode", "check":
		var b strings.Builder
		fmt.Fprintf(&b, "%s do \"$_smelt_file\" %s", qbin, a.Cmd)
		for _, arg := range a.Args {
			b.WriteString(" ")
			b.WriteString(arg)
		}
		if a.Cmd == "transcode" {
			b.WriteString(" --run-id \"$RUN_ID\" -y")
		}
		b.WriteString(dbFlag)
		b.WriteString(" || true")
		return b.String()
	default: // skip, copy, unknown
		return ":"
	}
}

// Parse reads only the >>> smelt:manifest v1 >>> block from a script.
// Returns an error if the block is absent, unclosed, or missing src.
func Parse(script string) (Program, error) {
	const open = "# >>> smelt:manifest v1 >>>"
	const closeMarker = "# <<< smelt:manifest <<<"

	start := strings.Index(script, open)
	if start < 0 {
		return Program{}, fmt.Errorf("smelt:manifest block not found")
	}
	end := strings.Index(script[start:], closeMarker)
	if end < 0 {
		return Program{}, fmt.Errorf("smelt:manifest block not closed")
	}
	block := script[start+len(open) : start+end]

	var p Program
	for _, raw := range strings.Split(block, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || !strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if line == "" {
			continue
		}

		key, val, _ := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)

		switch key {
		case "name":
			p.Name = val
		case "schedule":
			p.Schedule = val
		case "src":
			p.Src = val
		case "ext":
			if val != "" {
				p.Ext = strings.Split(val, ",")
			}
		case "rule":
			r, err := parseRuleLine(val)
			if err != nil {
				return Program{}, fmt.Errorf("rule %q: %w", val, err)
			}
			p.Rules = append(p.Rules, r)
		}
	}
	if p.Src == "" {
		return Program{}, fmt.Errorf("manifest missing src")
	}
	return p, nil
}

// renderRuleLine serialises a Rule to one manifest comment line:
//
//	"when <field> <op> <value> [and ...] do <action> [flags]"  or  "do <action>"
func renderRuleLine(r Rule) string {
	var b strings.Builder
	if len(r.When) > 0 {
		b.WriteString("when ")
		for i, c := range r.When {
			if i > 0 {
				b.WriteString(" and ")
			}
			fmt.Fprintf(&b, "%s %s %s", c.Field, c.Op, c.Value)
		}
		b.WriteString(" ")
	}
	b.WriteString("do ")
	b.WriteString(r.Do.Cmd)
	for _, a := range r.Do.Args {
		b.WriteString(" ")
		b.WriteString(a)
	}
	return b.String()
}

// ParseRule parses a single rule line in manifest syntax back into a Rule.
// It is the inverse of renderRuleLine and is used by cmd/workflow to parse
// --rule flag values.
func ParseRule(line string) (Rule, error) { return parseRuleLine(line) }

// parseRuleLine parses a single manifest rule line back into a Rule.
func parseRuleLine(line string) (Rule, error) {
	var r Rule
	line = strings.TrimSpace(line)

	if strings.HasPrefix(line, "when ") {
		rest := strings.TrimPrefix(line, "when ")
		doIdx := strings.Index(rest, " do ")
		if doIdx < 0 {
			return Rule{}, fmt.Errorf("missing 'do' in rule")
		}
		for _, part := range strings.Split(rest[:doIdx], " and ") {
			fields := strings.Fields(part)
			if len(fields) != 3 {
				return Rule{}, fmt.Errorf("condition %q: want 'field op value'", part)
			}
			if err := validateFieldOp(fields[0], fields[1]); err != nil {
				return Rule{}, fmt.Errorf("condition %q: %w", part, err)
			}
			r.When = append(r.When, Cond{Field: fields[0], Op: fields[1], Value: fields[2]})
		}
		line = "do " + rest[doIdx+4:]
	}

	if !strings.HasPrefix(line, "do ") {
		return Rule{}, fmt.Errorf("expected 'do <action>'")
	}
	parts := strings.Fields(strings.TrimPrefix(line, "do "))
	if len(parts) == 0 {
		return Rule{}, fmt.Errorf("missing action after 'do'")
	}
	r.Do.Cmd = parts[0]
	if len(parts) > 1 {
		r.Do.Args = parts[1:]
	}
	return r, nil
}

// renderMatchArgs produces the flag args for `smelt match "$f" <args>`.
func renderMatchArgs(conds []Cond) string {
	var b strings.Builder
	for _, c := range conds {
		if flagName := condToFlag(c.Field, c.Op); flagName != "" {
			b.WriteString(" ")
			b.WriteString(flagName)
			b.WriteString(" ")
			b.WriteString(shellQuote(c.Value))
		}
	}
	return b.String()
}

// validFieldOps enumerates the field/operator combinations smelt match
// actually registers flags for (see docs/WORKFLOW.md). condToFlag builds
// "--<field>-<op>" blindly, so an unlisted combo parses fine here but fails
// at script-run time with "unknown flag" — reject it up front instead.
var validFieldOps = map[string]map[string]bool{
	"codec":    {"eq": true, "ne": true},
	"audio":    {"eq": true, "ne": true},
	"ext":      {"eq": true, "ne": true},
	"height":   {"gt": true, "lt": true, "ge": true, "le": true},
	"width":    {"gt": true, "lt": true},
	"bitrate":  {"gt": true, "lt": true},
	"duration": {"gt": true, "lt": true},
}

// ValidateRule rejects a Rule containing any field/operator combination
// smelt match has no flag for. Callers that build Rules outside parseRuleLine
// (e.g. the server API, which decodes Rules straight from JSON) must call
// this explicitly — parseRuleLine is the only path that validates otherwise.
func ValidateRule(r Rule) error {
	for _, c := range r.When {
		if err := validateFieldOp(c.Field, c.Op); err != nil {
			return fmt.Errorf("condition %q: %w", c.Field+" "+c.Op+" "+c.Value, err)
		}
	}
	return nil
}

// validateFieldOp rejects field/operator combinations smelt match has no
// flag for, so an invalid --rule fails at `smelt workflow` render time
// instead of producing a script that errors at run time.
func validateFieldOp(field, op string) error {
	ops, known := validFieldOps[field]
	if !known {
		return fmt.Errorf("unknown field %q", field)
	}
	if !ops[op] {
		valid := make([]string, 0, len(ops))
		for o := range ops {
			valid = append(valid, o)
		}
		sort.Strings(valid)
		return fmt.Errorf("operator %q not valid for field %q (valid: %s)", op, field, strings.Join(valid, ", "))
	}
	return nil
}

// condToFlag maps a (field, op) pair to the corresponding smelt match flag.
// Callers must have already validated the combination with validateFieldOp.
func condToFlag(field, op string) string {
	if !validFieldOps[field][op] {
		return ""
	}
	return "--" + field + "-" + op
}
