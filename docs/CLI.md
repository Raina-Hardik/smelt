# smelt CLI Reference

All commands, flags, and exact `--help` output for smelt v1.0.

---

## Global Flags

These flags are accepted by every command.

| Flag | Type | Default | Description |
|---|---|---|---|
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

Flags:
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

Output files are written alongside the source as `<name>.smelt<ext>` during
transcoding, then renamed to `<name><ext>` (in-place) or left as
`<name>.smelt<ext>` (default). Use `--output-dir` to redirect all output to a
separate directory.

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
      --codec string        target video codec: h264|h265|av1|vp9 (default "h265")
      --crf int             constant rate factor 0-51; lower = higher quality (default 23)
      --dry-run             print transcode plan without executing anything
      --ext strings         file extensions to match (default [mkv,mp4,avi])
  -h, --help                help for transcode
      --inplace             replace original file after successful transcode
      --output-dir string   write output files to this directory instead of alongside source
      --preset string       ffmpeg encoding preset (default "medium")
      --profile string      named profile from config; overrides --codec, --crf, --preset
      --src string          source directory to scan (default ".")
      --workers int         maximum parallel transcode jobs; 0 = runtime.NumCPU() (default 0)

Global Flags:
      --config string       path to config file; searches ./config.yaml then $HOME/.config/smelt/config.yaml
      --log-format string   log output format: auto|json|pretty (default "auto")
      --log-level string    log level: debug|info|warn|error (default "info")
```

### Flag reference

| Flag | Type | Default | Description |
|---|---|---|---|
| `--codec` | string | `h265` | Target video codec. Valid values: `h264`, `h265`, `av1`, `vp9`. Maps to `libx264`, `libx265`, `libsvtav1`, `libvpx-vp9` respectively. |
| `--crf` | int | `23` | Constant Rate Factor controlling quality vs. file size. Lower values produce higher quality. Valid range: 0–51. |
| `--dry-run` | bool | `false` | Print the full transcode plan (source → destination, codec, CRF) without executing ffmpeg or writing any files. |
| `--ext` | strings | `mkv,mp4,avi` | Comma-separated list of file extensions to match during the directory scan. Case-insensitive. Leading dots optional. |
| `--inplace` | bool | `false` | After a successful transcode, atomically replace the original file with the output. The original is unrecoverable after this operation. |
| `--output-dir` | string | _(alongside source)_ | Write all output files into this directory, preserving the relative path structure from `--src`. Directory must exist. |
| `--preset` | string | `medium` | ffmpeg encoding preset. Faster presets trade quality for speed. Common values: `ultrafast`, `fast`, `medium`, `slow`, `veryslow`. |
| `--profile` | string | _(none)_ | Name of a profile defined in the `profiles` section of `config.yaml`. When set, overrides `--codec`, `--crf`, and `--preset`. |
| `--src` | string | `.` | Root directory to scan recursively for media files. |
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
      --codec string        target video codec: h264|h265|av1|vp9 (default "h265")
      --crf int             constant rate factor 0-51; lower = higher quality (default 23)
      --ext strings         file extensions to match (default [mkv,mp4,avi])
  -h, --help                help for tui
      --inplace             replace original file after successful transcode
      --output-dir string   write output files to this directory instead of alongside source
      --preset string       ffmpeg encoding preset (default "medium")
      --profile string      named profile from config; overrides --codec, --crf, --preset
      --src string          source directory to scan (default ".")
      --workers int         maximum parallel transcode jobs; 0 = runtime.NumCPU() (default 0)

Global Flags:
      --config string       path to config file; searches ./config.yaml then $HOME/.config/smelt/config.yaml
      --log-format string   log output format: auto|json|pretty (default "auto")
      --log-level string    log level: debug|info|warn|error (default "info")
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
| `--log-level` | `SMELT_LOG_LEVEL` |
| `--log-format` | `SMELT_LOG_FORMAT` |
| `--src` | `SMELT_SRC` |
| `--ext` | `SMELT_EXT` |
| `--codec` | `SMELT_CODEC` |
| `--crf` | `SMELT_CRF` |
| `--preset` | `SMELT_PRESET` |
| `--workers` | `SMELT_WORKERS` |
| `--inplace` | `SMELT_INPLACE` |
| `--output-dir` | `SMELT_OUTPUT_DIR` |
| `--profile` | `SMELT_PROFILE` |

Example:

```bash
SMELT_WORKERS=16 SMELT_CODEC=av1 smelt transcode --src /mnt/media
```
