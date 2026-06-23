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
│   workers   ‹ 16 ›                                     │
│                                                        │
│   [↑↓/tab] field  [←→] change  [enter] start  [q] abort│
└──────────────────────────────────────────────────────┘
```

| Key | Action |
|---|---|
| `↑`/`↓`, `k`/`j`, `tab`/`shift+tab` | Move between fields (`▸` marks the focused one) |
| `←`/`→`, `h`/`l` | Change the focused field's value |
| `enter` / `s` | Start transcoding with the shown settings |
| `q` / `Ctrl+C` | Abort without touching any files |

Editable fields: **codec** (`h264`/`h265`/`av1`/`vp9`), **crf** (0–51),
**preset**, **hwaccel** (`auto`/`none`/backend), **workers**. Changing codec or
hwaccel re-probes; the `hwaccel` row reads `resolving…` until the probe returns.
Each field maps 1:1 to its CLI flag — the screen adds no behavior the flags
can't express. For a destructive `--inplace` run, the confirmation prompt still
happens on the normal terminal *before* this screen.

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
│  q quit · Q force-quit · ↑↓/jk navigate · ? help                       │  ← Status bar
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
| `↑` / `k` | Move selection up in the File Queue panel |
| `↓` / `j` | Move selection down in the File Queue panel |
| `?` / `esc` | Toggle / close a help overlay showing all keybindings |

> **Not yet implemented:** `p` pause/resume. It requires the worker pool to gate
> the start of new jobs (without interrupting running ffmpeg). Tracked for a
> follow-up; until it ships it is intentionally absent from this screen so the
> help stays in lockstep with the code (Help-Driven Development).

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

---

## Terminal Requirements

The TUI uses the alternate screen buffer (`tea.WithAltScreen`) and requires
a terminal with at least 80 columns and 24 rows for comfortable viewing.
Smaller terminals will display a degraded but functional layout.

True-color support is not required; the gradient progress bars gracefully
degrade to 256-color and 16-color palettes.
