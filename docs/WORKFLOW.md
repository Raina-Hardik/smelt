# smelt Programs

A **program** is smelt's answer to the question: "what if the workflow _was_ the
script?" Rather than a proprietary YAML plugin stack or a locked-down job
definition, a smelt program is an ordered, per-file decision pipeline expressed
as a plain, human-editable shell script. You can read it, edit it, `sh` it, and
`diff` it. There is no hidden engine.

---

## Why programs, not YAML

Tools like Tdarr define workflows as opaque plugin chains: a series of conditionals
and actions bundled into a plugin ID that the engine interprets at run time. You
cannot easily see what the workflow does, you cannot test individual conditions
in a shell, and the engine is the only thing that can run it.

smelt inverts this. A program is compiled to a self-contained POSIX shell script.
The decision logic is `if`/`elif` chains using four small CLI primitives. The
script is the executable artifact — not a representation of one.

Benefits:

- **Transparent.** Read the script to know exactly what will run.
- **Testable.** Run `smelt match movie.mkv --codec-ne hevc` on a single file to
  check a condition without committing to a transcode.
- **Portable.** The script only needs `smelt` and `sh`. No daemon, no server.
- **Composable.** Pipe, cron, systemd, or run by hand — the script works the same way.
- **Trackable.** With `--run-id`, the dashboard sees every run in real time,
  whether kicked off by cron, the WebUI, or the command line.

---

## The four primitives

Programs are built on four CLI commands. Each does exactly one thing.

### `smelt each`

Scan a source directory and emit one resolved file path per line, in scan order.
This is the iterator: every file that a program will potentially act on comes
from `each`.

```sh
smelt each --src /mnt/media --ext mkv,mp4
```

With `--run-id`, `each` also registers the run and its full file list in the
history database _before_ any file is processed, so the dashboard can show
queue depth from the first moment.

### `smelt match`

Test one or more conditions against a single file. Exits `0` if all conditions
pass, `1` if any fail. Silent on mismatch — safe to use directly in `if`/`elif`.

```sh
smelt match movie.mkv --codec-ne hevc --height-gt 1080
```

Conditions are AND-combined. You can test codec, resolution, bitrate, audio
codec, file extension, and duration. See
[CLI.md](CLI.md#smelt-match) for the full condition list.

### `smelt do`

Apply a smelt subcommand to a single file. The `transcode` subcommand accepts
every flag that `smelt transcode` does. The `check` subcommand runs a health
probe.

```sh
smelt do movie.mkv transcode --codec h265 --crf 23 -y
smelt do movie.mkv check
```

`-y` is important in scripts: without it, `--inplace` would prompt for
confirmation on every file.

### `smelt finish-run`

Reconcile and close a tracked run. Marks any jobs still queued as `skipped`
(no rule matched them), any jobs still running as `failed`, then sets the run's
final status. This is emitted automatically at the end of every generated script;
you rarely need to call it by hand.

```sh
smelt finish-run --run-id "$RUN_ID"
```

---

## The generated script

`smelt workflow` (and, later, the WebUI) renders a program into a shell script.
Here is a complete example:

```sh
#!/bin/sh
# smelt program: nightly
# generated 2026-06-30T00:00:00Z by smelt v0.11.0
# schedule: 0 3 * * *
# This is a plain script — edit the smelt:manifest block, or the body, freely.

# >>> smelt:manifest v1 >>>
# name: nightly
# schedule: 0 3 * * *
# src: /mnt/media
# ext: mkv,mp4
# rule: when codec ne hevc and height gt 1080 do transcode --codec h265 --crf 24
# rule: when codec ne hevc do transcode --codec h265 --crf 23
# rule: do skip
# <<< smelt:manifest <<<

set -eu

LOCK="${TMPDIR:-/tmp}/smelt-nightly.lock"
if command -v flock >/dev/null 2>&1; then
	exec 9>"$LOCK"
	flock -n 9 || { echo "smelt program nightly: already running" >&2; exit 0; }
fi

if [ -t 1 ] && command -v gum >/dev/null 2>&1; then
	gum style --border normal "smelt: nightly"
fi

RUN_ID="${SMELT_RUN_ID:-$(date +%s)-$$}"

'/usr/bin/smelt' each --src '/mnt/media' --ext 'mkv,mp4' --name 'nightly' --run-id "$RUN_ID" --db '/home/user/.local/share/smelt/history.db' | while IFS= read -r _smelt_file; do
	if '/usr/bin/smelt' match "$_smelt_file" --codec-ne 'hevc' --height-gt '1080'; then
		'/usr/bin/smelt' do "$_smelt_file" transcode --codec h265 --crf 24 --run-id "$RUN_ID" -y --db '/home/user/.local/share/smelt/history.db' || true
	elif '/usr/bin/smelt' match "$_smelt_file" --codec-ne 'hevc'; then
		'/usr/bin/smelt' do "$_smelt_file" transcode --codec h265 --crf 23 --run-id "$RUN_ID" -y --db '/home/user/.local/share/smelt/history.db' || true
	else
		:
	fi
done

'/usr/bin/smelt' finish-run --run-id "$RUN_ID" --db '/home/user/.local/share/smelt/history.db'
```

Walk through:

1. **Lock guard.** `flock -n` acquires a non-blocking lock. If another instance
   is running the script exits cleanly with code `0` — cron treats it as success.
   `flock` is optional; the guard is skipped on systems without it.
2. **gum banner.** If the terminal is a TTY and `gum` is installed, a styled
   banner is shown. Never blocks cron — the `[ -t 1 ]` guard skips it entirely
   when stdout is not a terminal.
3. **RUN_ID.** Generated from `date +%s` and the PID if not provided via the
   environment. The WebUI sets `SMELT_RUN_ID` so dashboard-initiated runs appear
   under the correct run record.
4. **each | while loop.** `each` emits paths; the `while` loop iterates them.
   `IFS= read -r` preserves paths with spaces and special characters.
5. **match / do pairs.** Each rule is an `if`/`elif` branch: test with `match`,
   act with `do`. Rules are evaluated in order; the first matching rule wins.
6. **`|| true`.** Failure on a single file (non-zero exit from `do`) does not
   abort the loop. The failure is recorded in the DB and surfaced by `finish-run`.
7. **finish-run.** Runs unconditionally (outside the loop) because `set -eu` is
   in effect — the only early exits are the lock guard.

---

## The manifest block

```
# >>> smelt:manifest v1 >>>
# ...
# <<< smelt:manifest <<<
```

The manifest block is the machine-parseable source of truth for a program. smelt
reads ONLY this block when importing or re-rendering a script — it never parses
arbitrary bash. This means:

- You can freely reformat the shell body (rename variables, add echo statements,
  swap in your own logging) and smelt will still be able to round-trip the
  program.
- The manifest and the shell body can get out of sync if you edit the body
  manually. That is intentional and safe — the script you edited is what runs.
  Use `smelt workflow` to re-render from the manifest if you want them back in
  sync.

### Manifest keys

| Key | Description |
|---|---|
| `name` | Human-readable run label, also used in the lock file name. |
| `schedule` | Cron expression. Printed as a ready-to-paste `crontab` line by `smelt workflow --schedule`. |
| `src` | Source directory passed to `each`. |
| `ext` | Comma-separated extension list passed to `each`. |
| `rule` | One rule per line, in priority order (first match wins). |

---

## Rule syntax

A rule has the form:

```
[when <field> <op> <value> [and <field> <op> <value>...]] do <action> [flags]
```

Omit the `when` clause for a catch-all rule that matches every file.

### Fields

| Field | Matches |
|---|---|
| `codec` | Video codec (e.g. `hevc`, `h264`, `av1`, `vp9`) |
| `height` | Frame height in pixels |
| `width` | Frame width in pixels |
| `bitrate` | Overall bitrate in kbps |
| `audio` | First audio codec (e.g. `aac`, `opus`, `ac3`) |
| `ext` | File extension without leading dot (e.g. `mkv`) |
| `duration` | Duration in seconds |

### Operators

| Operator | Meaning |
|---|---|
| `eq` | equals |
| `ne` | does not equal |
| `gt` | greater than |
| `lt` | less than |
| `ge` | greater than or equal to |
| `le` | less than or equal to |

Not every operator is valid for every field — each field maps to a `smelt match`
flag, and `smelt match --help` only defines a subset of `--<field>-<op>` flags
per field:

| Field | Valid operators |
|---|---|
| `codec`, `audio`, `ext` | `eq`, `ne` |
| `height` | `gt`, `lt`, `ge`, `le` |
| `width`, `bitrate`, `duration` | `gt`, `lt` |

The rule parser does **not** enforce this — a rule like `when width le 1920 do
...` renders without error, but the generated script fails at run time with
`Error: unknown flag: --width-le`. Stick to the valid-operator table above
until this is validated at `smelt workflow` render time.

### Actions

| Action | Description |
|---|---|
| `transcode [flags]` | Transcode the file. Accepts all `smelt transcode` encode flags. |
| `skip` | Do nothing. Use as a catch-all to mark unmatched files as explicitly skipped. |
| `check` | Run a health probe on the file. |

### Examples

```
# Re-encode 4K HDR files with a high-quality preset
when codec ne hevc and height gt 2160 do transcode --codec h265 --crf 20 --preset slow

# Re-encode everything else that is not already HEVC
when codec ne hevc do transcode --codec h265 --crf 23

# Explicitly skip everything else (recorded as skipped, not absent)
do skip
```

First matching rule wins per file. If no rule matches and there is no catch-all
`do skip`, the file is left in the `queued` state and `finish-run` will mark it
as `skipped` at reconciliation time.

---

## The run lifecycle

Each run goes through four phases. With `--run-id` set throughout, every phase
is visible in the history dashboard in real time.

```
each --run-id $ID      →  run row created, all files queued
do --run-id $ID        →  per-file: queued → running → done/failed
finish-run --run-id $ID →  leftover queued → skipped, leftover running → failed
                            run row: status = done|failed, final counts set
```

This lifecycle is what makes program runs fully visible to the dashboard —
including runs triggered by cron or the shell, which Tdarr cannot see at all.

### Why this matters for reliability

- If the script is killed mid-run (`kill`, power loss, OOM), `finish-run` will
  not execute. The run stays open in the DB. The WebUI will surface it as a
  stalled run and you can close it manually.
- Files that `do` fails on exit with a non-zero code, captured by `|| true`.
  Their job rows are marked `failed` by the `do` call. `finish-run` counts them.
- Files that were never reached (e.g. the script was killed before iterating
  them) are left `queued`. `finish-run` marks them `skipped`.

---

## Creating a program

### With `smelt workflow`

The easiest path: `smelt workflow` accepts a rule set and renders the script.

```sh
# Print to stdout
smelt workflow --src /mnt/media --ext mkv,mp4 \
    --rule "when codec ne hevc and height gt 1080 do transcode --codec h265 --crf 24" \
    --rule "when codec ne hevc do transcode --codec h265 --crf 23"

# Write to a file with a cron schedule
smelt workflow --src /mnt/media --ext mkv,mp4 \
    --rule "when codec ne hevc do transcode --codec h265 --crf 23" \
    --name nightly \
    --schedule "0 3 * * *" \
    -o /etc/smelt/nightly.sh
```

The crontab line to install is printed when `--schedule` and `--out` are both
set:

```
0 3 * * * /etc/smelt/nightly.sh
```

### Writing the script by hand

Copy the template above, edit the manifest block and the shell body to match,
and make it executable. The only contract smelt enforces is the
`>>> smelt:manifest v1 >>>` … `<<< smelt:manifest <<<` delimiters, which it
needs if you later want to re-import the program.

---

## Optional niceties

### flock — overlap prevention

The `flock -n` guard ensures a second run of the script exits immediately if the
first is still going. This is important for cron: if a long media library scan
runs past its scheduled interval, the next invocation does not stack on top.

`flock` is detected at run time with `command -v flock`. On systems without it
(notably macOS and some minimal containers) the guard is skipped transparently.

### gum — styled banners

[gum](https://github.com/charmbracelet/gum) is an optional CLI prettifier. When
`gum` is on `$PATH` and stdout is a TTY, the script prints a styled banner at
the start of the run. This is purely cosmetic. It is never shown in cron output
(the `[ -t 1 ]` guard prevents it) and missing `gum` is handled by `command -v`.

### SMELT_RUN_ID

Set `SMELT_RUN_ID` in the environment before calling the script to associate the
run with a specific ID. The WebUI sets this when it launches scripts so that
dashboard-initiated runs appear under the correct run record rather than an
auto-generated one.

```sh
SMELT_RUN_ID=myrun-001 /etc/smelt/nightly.sh
```

---

## Quick reference

```sh
# Test a condition on a single file
smelt match /mnt/media/movie.mkv --codec-ne hevc --height-gt 1080
echo $?   # 0 = match, 1 = no match

# List all files that would be iterated
smelt each --src /mnt/media --ext mkv,mp4

# Transcode a single file
smelt do /mnt/media/movie.mkv transcode --codec h265 --crf 23 -y

# Generate and install a nightly script
smelt workflow \
    --src /mnt/media \
    --rule "when codec ne hevc do transcode --codec h265 --crf 23" \
    --name nightly \
    --schedule "0 3 * * *" \
    -o ~/smelt-nightly.sh
crontab -e   # paste the printed crontab line
```
