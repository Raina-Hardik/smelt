# smelt TUI Reference

The interactive TUI is launched with `smelt tui`. It accepts the same source,
codec, and worker flags as `smelt transcode`. On launch it scans the source and
opens a **pre-flight screen**; transcoding begins only once you confirm.

---

## Pre-flight screen

Before any job starts, smelt opens an editable **configure** screen. It shows
the static context (source + output target) and the encode settings as
adjustable controls, so you can tweak the run without restarting from the CLI.
It also surfaces the concrete encoder that `--hwaccel` resolved to (e.g.
`hevc_nvenc` vs. a software fallback), which is otherwise invisible until jobs
run.

```
┌──────────────────────────────────────────────────────┐
│ ⚡ smelt — configure                                   │
│                                                        │
│   src       ./testdata  (4 files)                      │
│   output    *.smelt.<ext>  (alongside source, …)       │
│                                                        │
│ ▸ codec     ‹ h265 ›                                   │
│   crf       ‹ 23 ›                                     │
│   preset    ‹ medium ›                                 │
│   hwaccel   ‹ auto › → hevc_nvenc                      │
│   hwdecode  ‹ auto ›                                   │
│   workers   ‹ 16 ›                                     │
│   decode    ‹ uncapped ›                               │
│   decode: hw: nvenc (per-file, may fall back)          │
│   audio     ‹ copy ›                                    │
│   bitrate   ‹ n/a ›                                     │
│   subs      ‹ copy ›                                    │
│   inplace   ‹ off ›                                     │
│                                                        │
│   [↑↓/tab] field  [←→] change  [enter] start  [q] abort│
└──────────────────────────────────────────────────────┘
```

| Key | Action |
|---|---|
| `↑`/`↓`, `k`/`j`, `tab`/`shift+tab` | Move between fields (`▸` marks the focused one) |
| `←`/`→`, `h`/`l` | Change the focused field's value |
| `enter` / `s` | Start transcoding with the shown settings (or, with `inplace` on, show the confirm screen below) |
| `q` / `Ctrl+C` | Abort without touching any files |

Editable fields, top to bottom:

| Field | Values | CLI flag |
|---|---|---|
| **codec** | `h264` / `h265` / `av1` / `vp9` | `--codec` |
| **crf** | `0`–`51` | `--crf` |
| **preset** | filtered to the resolved encoder — see table below | `--preset` |
| **hwaccel** | `auto` / `none` / backend | `--hwaccel` |
| **hwdecode** | `auto` / `off` | `--hwdecode` |
| **workers** | `1`–`256` | `--workers` |
| **decode** | `uncapped` or a thread count | `--decode-threads` |
| **audio** | `copy` / `aac` / `opus` / `mp3` / `ac3` / `flac` | `--audio-codec` |
| **bitrate** | `n/a` while `audio` is `copy`; otherwise `—` (encoder default) or a preset (`96k`…`320k`) | `--audio-bitrate` |
| **subs** | `copy` / `drop` | `--subs` |
| **inplace** | `off` / `on` (⚠ replaces originals) | `--inplace` |

Changing codec or hwaccel re-probes; the `hwaccel` row reads `resolving…`
until the probe returns. Per-file decode probes are keyed by backend, so
flipping `hwaccel` or `hwdecode` live re-probes correctly once the run starts. The `bitrate` field is a no-op while `audio` is
`copy` — matching the CLI, where `--audio-bitrate` is ignored in that case —
so it never queues a value that would never reach the ffmpeg invocation.

The **resource-profile** row (directly under `decode`) mirrors the same
resource-profile line `smelt transcode` logs via zerolog — the TUI doesn't
render log lines, so without this row it would be invisible here. It is
tri-state:

- `hw: <backend> (per-file, may fall back)` — a hardware encoder resolved on
  `nvenc`/`qsv`/`vaapi` and `hwdecode` is `auto`: decode runs on the same
  device for every file the per-file probe confirms; individual files may
  still fall back to software decode (probe miss or runtime retry). No
  warning — this is the full-hardware pipeline. Probes run per file at
  dispatch time, so the row never fakes certainty before they run.
- `software (capped at N threads)` / `software (uncapped)` with the ⚠
  warning appended — a hardware encoder resolved but decode is software
  (`hwdecode` set to `off`, or an encode-only `amf`/`videotoolbox` backend):
  uncapped software decode runs concurrently with the GPU/QSV/NVENC encode
  block, the most thermally demanding combination — see
  `--hwdecode`/`--decode-threads`/`--workers`.
- `software (…)` with no warning — software encode; nothing concurrent to
  warn about.

The **preset** list is filtered to the encoder that was actually resolved, so
you can only pick a value that encoder accepts:

| Resolved encoder | Presets offered |
|---|---|
| `libx264` / `libx265` (software) | `ultrafast … veryslow` |
| `*_nvenc` | `p1 … p7` |
| `*_qsv` | `veryfast … veryslow` |
| `libsvtav1` | `2 … 12` (SVT-AV1 numbers) |
| `libvpx-vp9`, `*_vaapi`, `*_amf`, `*_videotoolbox` | `n/a` (no preset) |

If a re-probe lands on an encoder for which the current preset isn't valid, it
snaps to that encoder's default. Each field maps 1:1 to its CLI flag — the
screen adds no behavior the flags can't express.

### What's launch-only, and why

Every `smelt transcode` flag works when passed to `smelt tui` — it just seeds
the pre-flight screen's starting values. Not every flag is *also* live-editable
on that screen, for two distinct reasons:

- **Determines which files are even in the queue.** `--ext`, `--force`,
  `--skip-hardlinked`, `--skip-source-codec`, `--profile`, `--ffmpeg-arg`, and
  `--i-know-this-drops-hdr` all act during the background scan/plan, before
  the pre-flight screen ever appears (`worker.Plan`, run once in `Init`).
  Changing them afterward would be a dead control: the file list
  (`m.files`) is fixed at that point, so toggling e.g.
  `--i-know-this-drops-hdr` on the configure screen could not retroactively
  un-block a Dolby Vision source already excluded from the queue. These stay
  launch-only on principle, not just for lack of time — exposing them would
  be misleading. Restart the TUI to rescan under new settings.
- **No text-input widget yet.** `--output-dir`, `--suffix`, and `--to`
  (container) only affect *how* an already-queued file is written, so they
  don't have the correctness problem above — but their values are
  free-form strings, and the pre-flight screen's editing model is a closed
  left/right cycle over a small choice set, not text entry. Wiring in a
  `bubbles/textinput`-style field is future work, not a correctness concern.

`codec` and `inplace` are live-editable despite *also* influencing the
original `worker.Plan` (in `--inplace` mode, codec drives the smart-skip
check; toggling `inplace` mid-session doesn't re-run that check either). This
is an accepted, pre-existing tradeoff for `codec` specifically — changing it
can leave the queue slightly stale relative to a fresh scan under the new
value (e.g. a file already in the newly-selected codec stays queued rather
than being dropped) — and `inplace` now shares that same tradeoff
deliberately, for consistency. Nothing is destroyed or corrupted either way:
`OutputPath`/`EncodeSpec` are computed fresh per file at transcode time from
whatever the fields read the moment the run starts, so worst case is
re-encoding a file that smart-skip would otherwise have spared. Restart the
TUI (or just don't change `codec`/`inplace` after reviewing the queue) if you
need the queue itself to reflect the new setting.

### `--inplace` confirmation

`--inplace` permanently replaces the original files, so pressing `enter`/`s`
with it set does not start the run — it opens a blocking confirm screen
first, the TUI equivalent of `smelt transcode`'s `y/N` prompt:

```
┌──────────────────────────────────────────────────────┐
│ ⚡ smelt — confirm                                     │
│                                                        │
│  --inplace will permanently replace 4 original files. │
│                                                        │
│  [y] continue   [any other key] cancel                │
└──────────────────────────────────────────────────────┘
```

| Key | Action |
|---|---|
| `y` / `Y` | Proceed with the run |
| anything else (including `esc`) | Cancel and return to the configure screen |

`-y`/`--assume-yes` skips this screen entirely, same as it skips the CLI's
prompt.

---

## Layout (while running)

```
┌─────────────────────────────────────────────────────────────────────────┐
│ ⚡ smelt              files: 12/47  errors: 0  workers: 8               │  ← Header
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  FILE QUEUE                                                             │  ← Queue panel
│  > The.Matrix.mkv                                       [pending]       │
│    Inception.mkv                                        [done   ]       │
│    Interstellar.mp4                                     [transcoding]   │
│    The.Dark.Knight.mkv                                  [pending]       │
│    ...                                                                  │
│                                                                         │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ACTIVE WORKERS                                                         │  ← Progress panel
│  Interstellar.mp4       ████████████░░░░░░░░░░░░  52%                  │
│  The.Dark.Knight.mkv    ████░░░░░░░░░░░░░░░░░░░░  18%                  │
│                                                                         │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  LOG                                                                    │  ← Log panel
│  ✓ Inception.mkv                                                        │
│  ✓ Memento.mkv                                                          │
│  ✗ Broken.avi: ffmpeg exit 1: no such encoder 'libx265'                │
│  waiting for workers…                                                   │
│                                                                         │
├─────────────────────────────────────────────────────────────────────────┤
│  q quit · Q force-quit · p pause · ↑↓/jk navigate · ? help             │  ← Status bar
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Panels

### Header

Displays the smelt logo and a live summary line:

- **files: N/M** — completed vs. total file count
- **errors: N** — count of files that failed transcoding
- **workers: N** — configured worker concurrency

The header is always visible and does not scroll.

### File Queue

A scrollable list of all discovered media files and their current status.

| Status badge | Meaning |
|---|---|
| `[pending]` | Waiting for a free worker slot |
| `[transcoding]` | Currently being processed by ffmpeg |
| `[done]` | Transcoded successfully |
| `[error]` | ffmpeg exited non-zero; details in the Log panel |
| `[cancelled]` | Stopped before finishing because the run was cancelled (`q`/`Q`) |

Navigate with `↑`/`↓` or `j`/`k`. The selected file is highlighted.

### Active Workers

Shows a live progress bar for each file currently being transcoded. Progress
is derived from the `time=HH:MM:SS.cs` lines emitted on ffmpeg's stderr,
compared against the file's total duration from `ffprobe`.

Each row shows:
- Filename (truncated to 32 characters)
- Animated gradient progress bar
- Percentage complete

When no files are actively transcoding this panel is hidden.

### Log

A rolling tail of the 8 most recent log events:

- `✓ <filename>` — successful completion
- `✗ <filename>: <reason>` — failure with a short error excerpt
- Status messages (`all done — N ok, N failed`)

Oldest entries are dropped as new ones arrive.

### Status Bar

Always-visible keybinding reminder at the bottom of the screen.

---

## Keybindings

| Key | Action |
|---|---|
| `enter` / `s` | (Pre-flight only) Start transcoding. |
| `q` / `Ctrl+C` | On the pre-flight screen, abort without touching any files. While running: cancel the run. In-flight ffmpeg processes are killed via context cancellation; smelt then waits for each worker to remove its partial `.transcoded` artifact before exiting. Files that hadn't finished show as `[cancelled]`, not `[error]`. |
| `Q` | Force-quit immediately. In-flight ffmpeg processes are killed, but smelt exits without waiting, so a partial `.transcoded` artifact from a killed job may be left behind. |
| `p` | Pause / resume the queue. Stops *starting* new jobs; jobs already running are not interrupted. The header shows `⏸ paused`. |
| `↑` / `k` | Move selection up in the File Queue panel |
| `↓` / `j` | Move selection down in the File Queue panel |
| `?` / `esc` | Toggle / close a help overlay showing all keybindings |

---

## Launching

```bash
# Basic launch — scans current directory for mkv/mp4/avi
smelt tui

# Specify source and codec
smelt tui --src /mnt/media --codec h265 --workers 8

# Use a named profile
smelt tui --src /mnt/media --profile archive
```

All `smelt transcode` flags except `--dry-run` are accepted.

---

## Quitting

Pressing `q` or `Ctrl+C` cancels the run's `context.Context`, which kills
active ffmpeg children (`exec.CommandContext` sends SIGKILL). smelt then waits
for the worker pool to drain — each cancelled job deletes its transient
`.transcoded` artifact — before exiting. Already-finished `.smelt` outputs are
kept; files that were still in flight are marked `[cancelled]`.

Press `Q` to skip that drain and exit immediately. ffmpeg is still killed, but
because smelt does not wait, a partial `.transcoded` file from an in-flight job
may remain on disk.

This all assumes `smelt` itself receives the keypress/signal. If `smelt` is
instead killed externally with `SIGKILL` (e.g. `kill -9`, or a process-group
`pkill -9` that catches the parent too) the Go cleanup above never runs at
all — `SIGKILL` isn't catchable — and a `.transcoded` partial can be left
behind regardless of which job was in flight. `smelt clean` finds and removes
these. Prefer `q`/`Ctrl+C` or `SIGTERM` when you have the choice.

---

## Terminal Requirements

The TUI uses the alternate screen buffer (`tea.WithAltScreen`) and requires
a terminal with at least 80 columns and 24 rows for comfortable viewing.
Smaller terminals will display a degraded but functional layout.

True-color support is not required; the gradient progress bars gracefully
degrade to 256-color and 16-color palettes.
