# smelt CLI Reference

All commands, flags, and exact `--help` output for smelt v1.0.

---

## Global Flags

These flags are accepted by every command.

| Flag | Type | Default | Description |
|---|---|---|---|
| `-y`, `--assume-yes` | bool | `false` | Skip confirmation prompts (assume yes) for destructive actions such as `--inplace`. |
| `--config` | string | _(auto-search)_ | Path to config file. Searches `./config.yaml` then `$HOME/.config/smelt/config.yaml` when empty. |
| `--log-format` | string | `auto` | Log output format: `auto` \| `json` \| `pretty`. `auto` selects `pretty` when stdout is a TTY, `json` otherwise. |
| `--log-level` | string | `info` | Log verbosity: `debug` \| `info` \| `warn` \| `error`. |

---

## `smelt`

```
Smelt is a highly parallel, ffmpeg-powered media transcoding CLI and TUI.

It scans a source directory, applies configured codec targets, and transcodes
files concurrently — with live progress in the TUI or structured log output
in daemon and pipe mode.

Usage:
  smelt [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  config      Manage smelt configuration
  help        Help about any command
  transcode   Transcode media files in a directory
  tui         Launch the interactive transcoding TUI
  version     Print smelt version information
  workflow    Generate a schedulable shell script for a transcode job

Flags:
  -y, --assume-yes          skip confirmation prompts (assume yes) for destructive actions
      --config string       path to config file; searches ./config.yaml then $HOME/.config/smelt/config.yaml
  -h, --help                help for smelt
      --log-format string   log output format: auto|json|pretty (default "auto")
      --log-level string    log level: debug|info|warn|error (default "info")

Use "smelt [command] --help" for more information about a command.
```

---

## `smelt transcode`

Scan a source directory recursively for media files matching the configured
extensions, then transcode each file using ffmpeg. Jobs run in parallel up to
`--workers`, with progress reported via zerolog.

During transcoding, output is written to a transient `<name>.transcoded<ext>`
artifact (deleted on any failure). On success it is renamed to its final path:
`<name><ext>` with `--inplace`, or `<name>.smelt<ext>` by default. Use
`--output-dir` to redirect all output into a separate directory.

### Synopsis

```
smelt transcode [flags]
```

### Examples

```bash
# Transcode all MKV and MP4 files to H.265 using 8 workers
smelt transcode --src /mnt/media --ext mkv,mp4 --codec h265 --workers 8

# Dry-run: print what would be transcoded, touch nothing
smelt transcode --src /mnt/media --dry-run

# Replace originals in-place with fine-tuned H.264
smelt transcode --src /mnt/media --inplace --codec h264 --crf 22 --preset fast

# Apply a named profile from config.yaml
smelt transcode --src /mnt/media --profile web

# Write transcoded files to a separate directory
smelt transcode --src /mnt/media --output-dir /mnt/transcoded
```

### `--help` output

```
Scan a source directory for media files matching the given extensions and
transcode each file using ffmpeg with the configured codec, CRF, and preset.
Jobs run in parallel up to --workers, with progress reported via zerolog.

Usage:
  smelt transcode [flags]

Examples:
  smelt transcode --src /mnt/media --ext mkv,mp4 --codec h265 --workers 8
  smelt transcode --src /mnt/media --dry-run
  smelt transcode --src /mnt/media --inplace --codec h264 --crf 22 --preset fast
  smelt transcode --src /mnt/media --profile web
  smelt transcode --src /mnt/media --output-dir /mnt/transcoded

Flags:
      --audio-bitrate string     audio bitrate when re-encoding, e.g. 192k (ignored when --audio-codec=copy)
      --audio-codec string       audio codec: copy|aac|opus|mp3|ac3|flac (copy = passthrough, no re-encode) (default "copy")
      --codec string             target video codec: h264|h265|av1|vp9 (default "h265")
      --crf int                  constant rate factor 0-51; lower = higher quality (default 23)
      --dry-run                  print transcode plan without executing anything
      --ext strings              file extensions to match (default [mkv,mp4,avi])
      --ffmpeg-arg stringArray   raw ffmpeg argument passed through verbatim; repeatable (e.g. --ffmpeg-arg=-vf --ffmpeg-arg=scale=1280:-2)
      --force                    re-transcode even if the output file already exists
  -h, --help                     help for transcode
      --hwaccel string           hardware acceleration: auto|none|nvenc|qsv|vaapi|amf|videotoolbox (default "auto")
      --inplace                  replace original after transcode; files already in the target codec are skipped (use --force to re-encode)
      --output-dir string        write output files to this directory instead of alongside source
      --preset string            encoding preset; normalized into the chosen encoder's namespace (e.g. x264 'superfast' → nvenc 'fast') (default "medium")
      --profile string           named profile preset from config; explicit flags still override it
      --skip-hardlinked          with --inplace, skip files that are hardlinked elsewhere (transcoding would break the link and double disk usage)
      --src string               source directory to scan (default ".")
      --suffix string            filename suffix for outputs written alongside the source (default ".smelt")
      --to string                target container/format for outputs: mp4|mkv|webm|... (default: keep source container)
      --workers int              maximum parallel transcode jobs; 0 = runtime.NumCPU()

Global Flags:
  -y, --assume-yes          skip confirmation prompts (assume yes) for destructive actions
      --config string       path to config file; searches ./config.yaml then $HOME/.config/smelt/config.yaml
      --log-format string   log output format: auto|json|pretty (default "auto")
      --log-level string    log level: debug|info|warn|error (default "info")
```

### Flag reference

| Flag | Type | Default | Description |
|---|---|---|---|
| `--audio-codec` | string | `copy` | Audio codec. `copy` stream-copies the audio untouched (no re-encode). Other values re-encode: `aac`, `opus` (→ `libopus`), `mp3` (→ `libmp3lame`), `ac3`, `flac`. |
| `--audio-bitrate` | string | _(encoder default)_ | Target audio bitrate when re-encoding, e.g. `192k`. Ignored when `--audio-codec=copy`. |
| `--codec` | string | `h265` | Target video codec. Valid values: `h264`, `h265`, `av1`, `vp9`. Maps to `libx264`, `libx265`, `libsvtav1`, `libvpx-vp9` respectively. |
| `--crf` | int | `23` | Constant Rate Factor controlling quality vs. file size. Lower values produce higher quality. Valid range: 0–51. |
| `--dry-run` | bool | `false` | Print the full transcode plan (source → destination, codec, CRF) without executing ffmpeg or writing any files. |
| `--ext` | strings | `mkv,mp4,avi` | Comma-separated list of file extensions to match during the directory scan. Case-insensitive. Leading dots optional. |
| `--ffmpeg-arg` | stringArray | _(none)_ | Raw ffmpeg argument passed through verbatim. Repeatable — one token per flag (e.g. `--ffmpeg-arg=-vf --ffmpeg-arg=scale=1280:-2`). Profile `extra_args` are prepended before these. |
| `--force` | bool | `false` | Re-transcode even when a file is already up to date. Without it, re-runs are idempotent: normal runs skip existing outputs (and smelt's own `<suffix>` files); `--inplace` skips files already in the target codec. `--force` disables both. |
| `--hwaccel` | string | `auto` | Hardware acceleration backend: `auto`, `none`, `nvenc`, `qsv`, `vaapi`, `amf`, `videotoolbox`. `auto` functionally probes for a usable GPU encoder for the target codec and falls back to software; an explicit backend that isn't usable also falls back. The chosen encoder is logged. |
| `--inplace` | bool | `false` | After a successful transcode, atomically replace the original file with the output. Files already in the target codec are skipped (probed with `ffprobe`; override with `--force`). The original is unrecoverable after this operation. Prompts for confirmation unless `-y`. Mutually exclusive with `--output-dir` and `--to`. |
| `--output-dir` | string | _(alongside source)_ | Write all output files into this directory, mirroring the relative path structure from `--src` and keeping the original filename. Created if missing. Mutually exclusive with `--inplace`. |
| `--preset` | string | `medium` | Encoding preset (speed/quality trade-off). Given an x264-style name (`ultrafast`…`veryslow`), it is normalized into the resolved encoder's namespace: NVENC → `fast`/`medium`/`slow` (or pass `p1`–`p7`), QSV → `veryfast`…`veryslow`, SVT-AV1 → a number `0`–`13`. Ignored for VP9/VAAPI/AMF/VideoToolbox. |
| `--profile` | string | _(none)_ | Name of a profile defined in the `profiles` section of `config.yaml`. Acts as a preset for `--codec`, `--crf`, `--preset`, and `extra_args`; explicit flags still take precedence over it. |
| `--skip-hardlinked` | bool | `false` | With `--inplace`, skip files whose hardlink count is >1. Transcoding replaces the inode, breaking the hardlink (e.g. to a torrent client's copy) and doubling disk usage — useful for ARR/seedbox setups. Overridden by `--force`. No effect without `--inplace`. |
| `--src` | string | `.` | Root directory to scan recursively for media files. |
| `--suffix` | string | `.smelt` | Filename suffix for outputs written alongside the source (`<name><suffix><ext>`). Ignored for `--inplace` and `--output-dir`. |
| `--to` | string | _(keep source)_ | Target output container/format, e.g. `mp4`, `mkv`, `webm`. Changes only the container (extension/muxer), not the codec. For mp4 it adds `+faststart` and tags HEVC as `hvc1`. Mutually exclusive with `--inplace`. |
| `--workers` | int | `0` | Maximum number of simultaneous ffmpeg processes. `0` means `runtime.NumCPU()`. |

---

## `smelt tui`

Launch the interactive bubbletea TUI. Scans the source directory and begins
transcoding using the same flag set as `smelt transcode` (minus `--dry-run`).
See [TUI.md](TUI.md) for the full layout and keybindings reference.

### Synopsis

```
smelt tui [flags]
```

### `--help` output

```
Launch the interactive transcoding TUI. Scans the source directory and begins
transcoding in parallel. Progress, worker status, and logs are displayed live.
Press q or Ctrl+C to quit; active jobs are cancelled cleanly.

Usage:
  smelt tui [flags]

Flags:
      --audio-bitrate string     audio bitrate when re-encoding, e.g. 192k (ignored when --audio-codec=copy)
      --audio-codec string       audio codec: copy|aac|opus|mp3|ac3|flac (copy = passthrough, no re-encode) (default "copy")
      --codec string             target video codec: h264|h265|av1|vp9 (default "h265")
      --crf int                  constant rate factor 0-51; lower = higher quality (default 23)
      --ext strings              file extensions to match (default [mkv,mp4,avi])
      --ffmpeg-arg stringArray   raw ffmpeg argument passed through verbatim; repeatable (e.g. --ffmpeg-arg=-vf --ffmpeg-arg=scale=1280:-2)
      --force                    re-transcode even if the output file already exists
  -h, --help                     help for tui
      --hwaccel string           hardware acceleration: auto|none|nvenc|qsv|vaapi|amf|videotoolbox (default "auto")
      --inplace                  replace original after transcode; files already in the target codec are skipped (use --force to re-encode)
      --output-dir string        write output files to this directory instead of alongside source
      --preset string            encoding preset; normalized into the chosen encoder's namespace (e.g. x264 'superfast' → nvenc 'fast') (default "medium")
      --profile string           named profile preset from config; explicit flags still override it
      --skip-hardlinked          with --inplace, skip files that are hardlinked elsewhere (transcoding would break the link and double disk usage)
      --src string               source directory to scan (default ".")
      --suffix string            filename suffix for outputs written alongside the source (default ".smelt")
      --to string                target container/format for outputs: mp4|mkv|webm|... (default: keep source container)
      --workers int              maximum parallel transcode jobs; 0 = runtime.NumCPU()

Global Flags:
  -y, --assume-yes          skip confirmation prompts (assume yes) for destructive actions
      --config string       path to config file; searches ./config.yaml then $HOME/.config/smelt/config.yaml
      --log-format string   log output format: auto|json|pretty (default "auto")
      --log-level string    log level: debug|info|warn|error (default "info")
```

---

## `smelt workflow`

Generates a self-contained, human-editable shell script that runs a `smelt
transcode`. There is no separate workflow engine — the script *is* the workflow.
It includes an overlap guard (`flock`) so scheduled runs never stack, and
expands the resolved config (including any `--profile`) into explicit flags so
the script doesn't depend on a config file at run time.

### Synopsis

Accepts every `smelt transcode` flag to define the job, plus:

| Flag | Type | Default | Description |
|---|---|---|---|
| `-o`, `--out` | string | _(stdout)_ | Write the script to this file and make it executable. |
| `--name` | string | `smelt-workflow` | Name used in the script header and the `flock` lock file. |
| `--schedule` | string | _(none)_ | Cron expression. Prints a ready-to-paste crontab line. Requires `--out`. |

### Examples

```bash
# Print a workflow script to stdout
smelt workflow --src /mnt/media --codec h265

# Write an executable nightly job and show the crontab line to install
smelt workflow --src /mnt/media --inplace -o nightly.sh --schedule "0 3 * * *"
```

### `--help` output

```
Generate a self-contained, human-editable shell script that runs a smelt
transcode. The script is plain — it IS the workflow; there is no separate engine.
It includes an overlap guard (flock) so scheduled runs never stack.

Accepts every 'smelt transcode' flag to define the job. With --out the script is
written to a file and made executable; otherwise it is printed to stdout. With
--schedule a ready-to-paste crontab line is printed (requires --out).

Usage:
  smelt workflow [flags]

Examples:
  smelt workflow --src /mnt/media --codec h265 -o nightly.sh
  smelt workflow --src /mnt/media --inplace -o nightly.sh --schedule "0 3 * * *"

Flags:
      --name string              workflow name, used in the script header and lock file
  -o, --out string               write the script to this file (made executable); default stdout
      --schedule string          cron expression to run the script on a timer, e.g. "0 3 * * *" (requires --out)
  (plus all `smelt transcode` flags)
```

---

## `smelt config`

Parent command for configuration management subcommands.

### `--help` output

```
Manage smelt configuration.

Usage:
  smelt config [command]

Available Commands:
  init        Write a default config.yaml to the current directory

Flags:
  -h, --help   help for config

Global Flags:
      --config string       path to config file; searches ./config.yaml then $HOME/.config/smelt/config.yaml
      --log-format string   log output format: auto|json|pretty (default "auto")
      --log-level string    log level: debug|info|warn|error (default "info")

Use "smelt config [command] --help" for more information about a command.
```

---

## `smelt config init`

Write a fully annotated `config.yaml` to the current working directory.

Fails with a non-zero exit code if `./config.yaml` already exists. Use
`--force` to overwrite.

### Synopsis

```
smelt config init [flags]
```

### `--help` output

```
Write a default config.yaml to the current working directory.
Fails if config.yaml already exists; use --force to overwrite.

Usage:
  smelt config init [flags]

Flags:
      --force   overwrite an existing config.yaml
  -h, --help    help for init

Global Flags:
      --config string       path to config file; searches ./config.yaml then $HOME/.config/smelt/config.yaml
      --log-format string   log output format: auto|json|pretty (default "auto")
      --log-level string    log level: debug|info|warn|error (default "info")
```

### Exit codes

| Code | Meaning |
|---|---|
| 0 | config.yaml written successfully |
| 1 | config.yaml already exists and `--force` was not set |
| 2 | Failed to write file (permissions, disk full, etc.) |

---

## `smelt version`

Print smelt version, Go runtime version, and OS/architecture.

### `--help` output

```
Print smelt version information.

Usage:
  smelt version [flags]

Flags:
  -h, --help   help for version

Global Flags:
      --config string       path to config file; searches ./config.yaml then $HOME/.config/smelt/config.yaml
      --log-format string   log output format: auto|json|pretty (default "auto")
      --log-level string    log level: debug|info|warn|error (default "info")
```

### Example output

```
smelt v1.0.0 (go1.23.0, linux/amd64)
```

---

## Environment Variables

Every flag can be set via an environment variable prefixed with `SMELT_`, using
uppercase and underscores. Flag precedence (highest to lowest):

1. CLI flag
2. Environment variable
3. `config.yaml` value
4. Built-in default

| Flag | Env var |
|---|---|
| `--assume-yes` | `SMELT_ASSUME_YES` |
| `--log-level` | `SMELT_LOG_LEVEL` |
| `--log-format` | `SMELT_LOG_FORMAT` |
| `--src` | `SMELT_SRC` |
| `--ext` | `SMELT_EXT` |
| `--hwaccel` | `SMELT_HWACCEL` |
| `--force` | `SMELT_FORCE` |
| `--suffix` | `SMELT_SUFFIX` |
| `--to` | `SMELT_TO` |
| `--codec` | `SMELT_CODEC` |
| `--crf` | `SMELT_CRF` |
| `--preset` | `SMELT_PRESET` |
| `--audio-codec` | `SMELT_AUDIO_CODEC` |
| `--audio-bitrate` | `SMELT_AUDIO_BITRATE` |
| `--workers` | `SMELT_WORKERS` |
| `--inplace` | `SMELT_INPLACE` |
| `--skip-hardlinked` | `SMELT_SKIP_HARDLINKED` |
| `--output-dir` | `SMELT_OUTPUT_DIR` |
| `--profile` | `SMELT_PROFILE` |

Example:

```bash
SMELT_WORKERS=16 SMELT_CODEC=av1 smelt transcode --src /mnt/media
```
