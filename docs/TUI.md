# smelt TUI Reference

The interactive TUI is launched with `smelt tui`. It accepts the same source,
codec, and worker flags as `smelt transcode` and begins processing immediately.

---

## Layout

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
│  q quit   ↑↓/jk navigate   p pause   ? help                           │  ← Status bar
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
| `q` / `Ctrl+C` | Quit smelt. Active ffmpeg processes are cancelled via context cancellation and waited on before exit. |
| `Q` | Force-quit immediately. Active ffmpeg processes receive SIGKILL. Output files in progress are deleted. |
| `↑` / `k` | Move selection up in the File Queue panel |
| `↓` / `j` | Move selection down in the File Queue panel |
| `p` | Pause / resume the job queue. Does not interrupt actively running ffmpeg jobs; prevents new jobs from starting. |
| `?` | Toggle a help overlay showing all keybindings |

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

Pressing `q` or `Ctrl+C` sends a cancellation signal to the worker pool.
Active ffmpeg processes receive the OS signal mapped from Go's
`context.Context` cancellation (SIGKILL on Linux). Partial output files
(identifiable by the `.smelt` suffix) are automatically removed.

Press `Q` to skip the clean-shutdown wait and exit immediately.

---

## Terminal Requirements

The TUI uses the alternate screen buffer (`tea.WithAltScreen`) and requires
a terminal with at least 80 columns and 24 rows for comfortable viewing.
Smaller terminals will display a degraded but functional layout.

True-color support is not required; the gradient progress bars gracefully
degrade to 256-color and 16-color palettes.
