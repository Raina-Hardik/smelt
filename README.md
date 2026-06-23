# smelt

[![CI](https://img.shields.io/github/actions/workflow/status/Raina-Hardik/smelt/ci.yml?branch=master&label=CI)](https://github.com/Raina-Hardik/smelt/actions)
[![Go Version](https://img.shields.io/github/go-mod/go-version/Raina-Hardik/smelt)](go.mod)
[![License: ISC](https://img.shields.io/badge/License-ISC-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/Raina-Hardik/smelt)](https://github.com/Raina-Hardik/smelt/releases/latest)

Highly parallel, ffmpeg-powered media transcoding CLI and TUI. Point it at a
media library, configure codec targets and concurrency, and smelt transcodes
everything — fast, observable, and cancellable.

---

## Features

- **Parallel transcoding** — semaphore-gated worker pool, configurable to any concurrency level
- **Hardware acceleration** — `--hwaccel auto` functionally probes for a usable GPU encoder (NVENC / QSV / VAAPI / AMF / VideoToolbox) and falls back to software
- **Interactive TUI** — editable pre-flight screen, live per-file progress bars, file queue, pause/resume, clean cancellation
- **Audio control** — stream-copy by default, or re-encode with `--audio-codec` / `--audio-bitrate`
- **Container conversion** — retarget the container with `--to mp4|mkv|webm` (mp4 gets faststart + hvc1)
- **Idempotent re-runs** — skips files already produced, or (with `--inplace`) files already in the target codec
- **Hardlink-aware** — `--skip-hardlinked` spares files hardlinked elsewhere (e.g. a seedbox/ARR setup)
- **Named profiles** — define `web`, `archive`, and custom codec/CRF/preset combinations in `config.yaml`
- **Workflows** — `smelt workflow` emits a schedulable, flock-guarded shell script (cron-friendly)
- **Dry-run mode** — inspect the full transcode plan without touching any file
- **In-place replacement** — atomically replaces originals after a confirmed successful transcode
- **Context-aware cancellation** — Ctrl+C / `q` cleanly kills all in-flight ffmpeg processes and removes partial output

---

## Requirements

| Dependency | Minimum |
|---|---|
| Go | 1.26 |
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

# Transcode to H.265 on the GPU if available, 8 workers
smelt transcode --src /mnt/media --ext mkv,mp4 --codec h265 --hwaccel auto --workers 8

# Re-encode audio to AAC alongside the video
smelt transcode --src /mnt/media --codec h265 --audio-codec aac --audio-bitrate 192k

# Convert the container to mp4 (faststart + hvc1 for HEVC)
smelt transcode --src /mnt/media --codec h265 --to mp4

# Replace originals in-place; already-h265 files are skipped automatically
smelt transcode --src /mnt/media --inplace --codec h265 --skip-hardlinked

# Use a named profile from config.yaml
smelt transcode --src /mnt/media --profile archive

# Launch the interactive TUI (editable pre-flight, live progress, p to pause)
smelt tui --src /mnt/media

# Generate a schedulable nightly workflow script
smelt workflow --src /mnt/media --inplace -o nightly.sh --schedule "0 3 * * *"

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
  hwaccel: auto
  audio_codec: copy
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

ISC — see [LICENSE](LICENSE).
