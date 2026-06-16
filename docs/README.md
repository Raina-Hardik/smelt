# smelt ⚡

[![CI](https://img.shields.io/github/actions/workflow/status/Raina-Hardik/smelt/ci.yml?branch=main&label=CI)](https://github.com/Raina-Hardik/smelt/actions)
[![Go Version](https://img.shields.io/github/go-mod/go-version/Raina-Hardik/smelt)](../go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](../LICENSE)
[![Release](https://img.shields.io/github/v/release/Raina-Hardik/smelt)](https://github.com/Raina-Hardik/smelt/releases/latest)

**smelt** is a highly parallel, ffmpeg-powered media transcoding CLI and TUI.  
Point it at a media library directory, configure codec targets and concurrency,
and smelt violently transcodes everything — fast, smart, and observable.

---

## Features

- **Parallel transcoding** — semaphore-gated worker pool, configurable to any
  concurrency level
- **Interactive TUI** — live per-file progress bars, file queue list, worker
  slot indicators, scrollable log tail
- **Named profiles** — define `web`, `archive`, and custom codec/CRF/preset
  combinations in `config.yaml`
- **Structured logging** — JSON in daemon/pipe mode; colorized pretty output
  when stdout is a TTY
- **Dry-run mode** — inspect the full transcode plan without touching any file
- **In-place replacement** — atomically replaces originals after a confirmed
  successful transcode
- **Context-aware cancellation** — Ctrl+C cleanly kills in-flight ffmpeg
  processes

---

## Requirements

| Dependency | Minimum version |
|---|---|
| Go | 1.23 |
| ffmpeg | 4.4 |
| ffprobe | 4.4 (bundled with ffmpeg) |

Both `ffmpeg` and `ffprobe` must be on `$PATH`.

---

## Install

### From source

```bash
git clone https://github.com/Raina-Hardik/smelt
cd smelt
go build -o smelt .
sudo mv smelt /usr/local/bin/
```

### With go install

```bash
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

smelt reads `./config.yaml` by default (override with `--config`).  
Run `smelt config init` to write a fully annotated starter file.

See [CONFIG.md](CONFIG.md) for the complete schema.

---

## Documentation

| Document | Contents |
|---|---|
| [CLI.md](CLI.md) | Every command, every flag, exact `--help` output |
| [CONFIG.md](CONFIG.md) | `config.yaml` schema — every key, type, default, and example |
| [TUI.md](TUI.md) | TUI layout, panel descriptions, keybindings |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Internal package map and end-to-end data flow |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Dev setup, test workflow, how to add a transcode profile |

---

## License

MIT — see [LICENSE](../LICENSE).
