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

## Running politely on constrained hardware

`--workers` bounds how many *files* run concurrently — it does nothing for a
single large file, where one ffmpeg process is free to use every core and
thread it can get. That matters most for a big software-decoded source (e.g.
10-bit 4K HEVC) on thin-and-light or otherwise thermally limited hardware.

smelt only ever accelerates the *encode* side. Decode is always software, on
every `--hwaccel` backend, so a resolved hardware encoder means full-CPU
decode running concurrently with the GPU/QSV/NVENC encode block — the most
demanding combination thermally. smelt logs a `resource profile` warning
whenever this combination resolves (including on `--dry-run`, before any file
is touched), and `--decode-threads N` caps the decoder's own thread count
(unlike `--ffmpeg-arg -threads N`, which lands after `-i` and only constrains
the encoder).

Beyond that, smelt has no CPU/thermal governor of its own — throttle at the OS
level:

```bash
# Cap CPU quota and I/O priority for one run via systemd (cgroup-enforced,
# unlike CPU affinity + nice/ionice chained through a shell, which can silently
# fail to inherit across exec boundaries — verify with `taskset -p $$` inside
# the actual ffmpeg process if you go that route)
systemd-run --scope -p CPUQuota=50% --nice=10 \
  smelt transcode --src /mnt/media --decode-threads 2 --workers 1

# Lower scheduling/IO priority only (no hard cap)
nice -n 10 ionice -c2 -n7 smelt transcode --src /mnt/media --decode-threads 2
```

If a run needs to be stopped, prefer `Ctrl-C`/`q` (in the TUI) or `SIGTERM`
over externally `SIGKILL`-ing the `smelt` process itself. smelt's own
cancellation path kills the in-flight ffmpeg child and then cleans up the
in-progress `.transcoded` partial in Go — but `SIGKILL` is not catchable, so
sending it straight to `smelt` (e.g. `pkill -9` on a whole process-group chain)
can end the Go process before that cleanup runs, leaving a stray partial file
behind (`smelt clean` finds and removes these). A hung ffmpeg child can also
occasionally outlive a first `SIGKILL` attempt (e.g. stuck in
`futex_do_wait`) and need a second, direct one.

---

## Planned / Under Consideration

The core tool targets straightforward container/codec transcoding. Complex
source material — Dolby Vision, HDR10(+), multi-layer/multi-track editorial
formats — has failure modes the simple ffmpeg pipeline doesn't handle: a
naive re-encode silently drops the DV RPU layer, mangles mastering-display
metadata, or otherwise degrades the source. These are real gaps, not
oversights, and they're intentionally out of scope for now.

Ideas being considered for a later "surgical mode" — a separate,
explicitly-opted-into path for source-aware handling, distinct from the
default transcode flow:

- **Dolby Vision passthrough** — extract/inject RPU via `dovi_tool` around
  the encode (profile 8.1 today; others later) instead of dropping the layer.
- **HDR10/HDR10+ metadata preservation** — carry mastering-display and
  content-light-level (and HDR10+ dynamic) metadata through re-encodes
  instead of losing it.
- **Source-complexity detection** — probe for these cases up front and warn
  (or refuse) rather than silently producing a degraded file.
- Possible shape: a `--preserve-dv` / `--surgical` flag or a dedicated
  `smelt` subcommand that opts into the extra external-tool pipeline, kept
  separate from the default single-ffmpeg-invocation flow so the common case
  stays simple and dependency-light.

Nothing here is scheduled — this is a place to collect ideas before design
work starts.

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

---

## AI Disclosure

Initial project scaffolding generated using Claude Sonnet 4.6.
- `#84e9215` docs: add full API-first surface documentation and lock CLI
- `#6ce437f` feat: initial project scaffold
