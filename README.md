# smelt

[![CI](https://img.shields.io/github/actions/workflow/status/Raina-Hardik/smelt/ci.yml?branch=main&label=CI)](https://github.com/Raina-Hardik/smelt/actions)
[![Go Version](https://img.shields.io/github/go-mod/go-version/Raina-Hardik/smelt)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/Raina-Hardik/smelt)](https://github.com/Raina-Hardik/smelt/releases/latest)

Highly parallel, ffmpeg-powered media transcoding CLI and TUI. Point it at a
media library, configure codec targets and concurrency, and smelt transcodes
everything — fast, observable, and cancellable.

---

## Features

- **Parallel transcoding** — semaphore-gated worker pool, configurable to any concurrency level
- **Interactive TUI** — live per-file progress bars, file queue, worker slot indicators, scrollable log tail
- **Named profiles** — define `web`, `archive`, and custom codec/CRF/preset combinations in `config.yaml`
- **Structured logging** — JSON in daemon/pipe mode; colorized output when stdout is a TTY
- **Dry-run mode** — inspect the full transcode plan without touching any file
- **In-place replacement** — atomically replaces originals after a confirmed successful transcode
- **Context-aware cancellation** — Ctrl+C cleanly kills all in-flight ffmpeg processes

---

## Requirements

| Dependency | Minimum |
|---|---|
| Go | 1.23 |
| ffmpeg | 4.4 |
| ffprobe | 4.4 (bundled with ffmpeg) |

Both `ffmpeg` and `ffprobe` must be on `$PATH`.

---

## Install

```bash
# From source
git clone https://github.com/Raina-Hardik/smelt
cd smelt
go build -o smelt .
sudo mv smelt /usr/local/bin/

# With go install
go install github.com/Raina-Hardik/smelt@latest
```

---

## Quick Start

```bash
# See what would be transcoded — touches nothing
smelt transcode --src /mnt/media --ext mkv,mp4 --dry-run

# Transcode to H.265 using 8 workers
smelt transcode --src /mnt/media --ext mkv,mp4 --codec h265 --workers 8

# Replace originals in-place after success
smelt transcode --src /mnt/media --inplace

# Use a named profile from config.yaml
smelt transcode --src /mnt/media --profile archive

# Launch the interactive TUI
smelt tui --src /mnt/media

# Generate a starter config.yaml
smelt config init
```

---

## Configuration

smelt reads `./config.yaml` by default (override with `--config`). Generate a
fully annotated starter with `smelt config init`.

```yaml
smelt:
  workers: 4

transcode:
  src: /mnt/media
  codec: h265
  crf: 23
  preset: medium
  inplace: false

profiles:
  web:
    codec: h264
    crf: 28
    preset: fast
    extra_args: ["-movflags", "+faststart"]
  archive:
    codec: h265
    crf: 18
    preset: slow
```

Every config key can also be set via a `SMELT_`-prefixed environment variable
or a CLI flag. Precedence: `CLI flag > env var > config.yaml > built-in default`.

See [docs/CONFIG.md](docs/CONFIG.md) for the full schema and key reference.

---

## Documentation

| Document | Contents |
|---|---|
| [docs/CLI.md](docs/CLI.md) | Every command, every flag, exact `--help` output |
| [docs/CONFIG.md](docs/CONFIG.md) | `config.yaml` schema — every key, type, default, and example |
| [docs/TUI.md](docs/TUI.md) | TUI layout, panel descriptions, keybindings |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Internal package map and end-to-end data flow |
| [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) | Dev setup, test workflow, how to add a transcode profile |

---

## License

MIT — see [LICENSE](LICENSE).
