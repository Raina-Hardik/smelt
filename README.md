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
- **Hardware acceleration** — `--hwaccel auto` functionally probes for a usable GPU encoder (NVENC / QSV / VAAPI / AMF / VideoToolbox) and falls back to software; on NVENC/QSV/VAAPI, decode runs on the same device too when a per-file probe confirms the source is decodable there (`--hwdecode auto`, opt out with `off`)
- **Interactive TUI** — editable pre-flight screen, live per-file progress bars, file queue, pause/resume, clean cancellation
- **Audio control** — stream-copy by default, or re-encode with `--audio-codec` / `--audio-bitrate`
- **Container conversion** — retarget the container with `--to mp4|mkv|webm` (mp4 gets faststart + hvc1)
- **Idempotent re-runs** — skips files already produced, or (with `--inplace`) files already in the target codec
- **Hardlink-aware** — `--skip-hardlinked` spares files hardlinked elsewhere (e.g. a seedbox/ARR setup)
- **Profiles** — ready-made flag sets (`web`, `web-hevc`, `archive`, `av1`, `mobile`) built into the binary; `smelt profiles` lists them, `smelt profiles show <name>` prints the exact flags each expands to, and `config.yaml` can override a built-in or add your own
- **Workflows** — `smelt workflow` emits a schedulable, flock-guarded shell script (cron-friendly)
- **Continuous watch** — `smelt watch` polls a directory on a timer and transcodes only new or changed files, using the same skip logic as a plain `transcode` run
- **HTTP API** — `smelt serve` exposes per-file decision programs, run triggering, and live progress over REST for a dashboard WebUI. The API is spec-first: `api/openapi.yaml` is the contract, the server is generated from it, and it's served live at `GET /openapi.yaml` with an interactive Scalar reference UI at `GET /docs`
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

`Raina-Hardik` is case-sensitive — the module path is declared with that exact
casing, and `raina-hardik` fails to resolve.

Right after a new tag is pushed, `@latest` can resolve to a stale prior version
for a while: the public Go module proxy caches `go list -m -versions` and can
lag behind GitHub's tags. If `go install ...@latest` doesn't pick up a release
you know exists, pin the version explicitly instead of waiting on the proxy:

```bash
GOPROXY=https://proxy.golang.org,direct go install github.com/Raina-Hardik/smelt@v0.17.0
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

# Use a built-in profile (no config needed); `smelt profiles` lists them all
smelt transcode --src /mnt/media --profile archive

# Launch the interactive TUI (editable pre-flight, live progress, p to pause)
smelt tui --src /mnt/media

# Generate a schedulable nightly workflow script
smelt workflow --src /mnt/media --inplace -o nightly.sh --schedule "0 3 * * *"

# Watch a directory continuously, transcoding new files every 5 minutes
smelt watch --src /mnt/media --codec h265 --interval 5m

# Start the HTTP API for a dashboard WebUI (binds to localhost only by default)
smelt serve --addr 127.0.0.1:7700

# Generate a starter config.yaml
smelt config init
```

---

## Docker

Every tagged release publishes a multi-arch (`linux/amd64`, `linux/arm64`)
image to GHCR, built on Alpine with `ffmpeg` preinstalled:

```bash
docker pull ghcr.io/raina-hardik/smelt:latest

# One-shot transcode: mount the media directory, override the entrypoint's
# implicit subcommand with your own
docker run --rm -v /mnt/media:/media ghcr.io/raina-hardik/smelt:latest \
    transcode --src /media --codec h265 --inplace -y

# Run smelt serve for the dashboard WebUI: publish the port, and persist the
# history DB and rendered program scripts outside the container
docker run -d --name smelt-serve -p 7700:7700 \
    -v smelt-data:/data \
    ghcr.io/raina-hardik/smelt:latest \
    serve --addr 0.0.0.0:7700 --db /data/history.db --scripts-dir /data/scripts
```

The image has no hardware-acceleration device passthrough configured by
default; add `--device /dev/dri` (VAAPI/QSV) or the NVIDIA container runtime
(CUDA/NVENC) if you need `--hwaccel` — and hardware decode with it — inside
the container. Alpine's ffmpeg build may also lack hardware acceleration
support entirely; smelt's functional probes make this safe (everything falls
back to software), it just won't be accelerated.

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

# web, web-hevc, archive, av1, mobile are built in — no config needed.
# Define a profile here to override a built-in field-by-field, or add your own:
profiles:
  web:
    crf: 20            # keep the built-in web profile, just raise quality
  hevc-1080p:          # a profile of your own; keys mirror transcode.* flags
    codec: h265
    crf: 22
    preset: slow
    to: mkv
    extra_args: ["-vf", "scale=-2:1080"]
```

Most config keys can also be set via a `SMELT_`-prefixed environment variable
or the equivalent CLI flag. Precedence: `CLI flag > env var > config.yaml >
built-in default` — see [docs/CLI.md](docs/CLI.md#environment-variables) for
the full flag/env var table.

See [docs/CONFIG.md](docs/CONFIG.md) for the full schema and key reference.

---

## Running politely on constrained hardware

`--workers` bounds how many *files* run concurrently — it does nothing for a
single large file, where one ffmpeg process is free to use every core and
thread it can get. That matters most for a big software-decoded source (e.g.
10-bit 4K HEVC) on thin-and-light or otherwise thermally limited hardware.

The first remedy is hardware decode. When a hardware encoder resolves on
`nvenc`, `qsv`, or `vaapi`, smelt also decodes on that *same device* for
every file a per-file probe confirms is decodable there (`--hwdecode auto`,
the default): the whole video pipeline stays in device memory and the
CPU-heavy decode disappears entirely. Files the device can't decode — and
everything with `--hwdecode off`, or on the encode-only `amf`/`videotoolbox`
backends — fall back to software decode, and that combination (full-CPU
decode running concurrently with the GPU/QSV/NVENC encode block) is the most
demanding thermally. smelt logs a `resource profile` warning whenever it
resolves (including on `--dry-run`, before any file is touched). For those
software-decoded files the fallback remedy is `--decode-threads N`, which
caps the decoder's own thread count (unlike `--ffmpeg-arg -threads N`, which
lands after `-i` and only constrains the encoder); it is omitted from the
command while hardware decode is active.

With hardware decode active, GPU memory becomes the constrained resource
instead: many concurrent 4K surfaces can exhaust VRAM (or hit NVDEC session
limits on consumer GeForce cards), which surfaces as per-file failures that
auto-retry in software — if that happens at scale, lower `--workers`.

Beyond that, smelt has no CPU/thermal governor of its own, on any platform —
`--hwdecode`, `--decode-threads`, and `--workers` are the levers smelt itself
gives you;
everything past that is OS-level throttling, and which tool that means
depends on what you're running:

```bash
# systemd-based Linux only (cgroup-enforced, unlike CPU affinity + nice/ionice
# chained through a shell, which can silently fail to inherit across exec
# boundaries — verify with `taskset -p $$` inside the actual ffmpeg process
# if you go that route). Not applicable on non-systemd inits (e.g. Void's
# runit) or on Windows/macOS.
systemd-run --scope -p CPUQuota=50% --nice=10 \
  smelt transcode --src /mnt/media --decode-threads 2 --workers 1

# Lower scheduling + I/O priority only (no hard cap) — Linux only (ionice is
# util-linux; macOS has `nice` but no `ionice` equivalent)
nice -n 10 ionice -c2 -n7 smelt transcode --src /mnt/media --decode-threads 2

# macOS: `nice` only (no ionice equivalent)
nice -n 10 smelt transcode --src /mnt/media --decode-threads 2

# Windows: no direct nice/ionice equivalent; lower the process priority instead
start /low /wait smelt transcode --src D:\media --decode-threads 2
```

The `resource profile` warning itself leads with the applicable remedy
(remove `--hwdecode off`, or a note that the source isn't hw-decodable on the
resolved backend), then suggests `--decode-threads`/`--workers`
unconditionally, since those are the only levers guaranteed to exist
everywhere smelt runs; it adds the `systemd-run` example on top only when
`systemd-run` is actually found on `$PATH`.

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

A separate idea, for thermally-limited hardware rather than complex source
material: **chunked/segmented transcode** — split a large file at keyframe
boundaries, transcode each segment as its own ffmpeg invocation with a
cool-down gap in between, then concatenate. Distinct from a thermal governor
(see [Running politely on constrained hardware](#running-politely-on-constrained-hardware))
because it bounds the *duration* of any single ffmpeg process rather than
trying to cap its resource usage while it runs; the two are complementary, not
alternatives. Not designed yet.

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

Community health files generated using Claude Sonnet 5 (Claude Code).
- `#910c157` docs: add code of conduct, PR template, and issue templates
