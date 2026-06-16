# smelt Architecture

End-to-end design of smelt: package ownership, data flow, cancellation, and
progress propagation.

---

## Package Map

```
smelt/
├── main.go                  Entry point — calls cmd.Execute()
├── cmd/                     Cobra command tree; no business logic
│   ├── root.go              Root command, persistent flags, zerolog/viper init
│   ├── transcode.go         `smelt transcode` — delegates to scanner + worker
│   ├── tui.go               `smelt tui` — delegates to scanner + tui.Model
│   ├── config.go            `smelt config init` — writes starter config.yaml
│   └── version.go           `smelt version` — prints build metadata
└── internal/                All business logic; not importable outside the module
    ├── config/
    │   └── config.go        Config struct + Load() — reads viper state
    ├── scanner/
    │   └── scanner.go       Scan() — doublestar walk → []MediaFile
    ├── ffmpeg/
    │   └── runner.go        Run() — exec wrapper, progress parsing, typed errors
    ├── worker/
    │   └── pool.go          Pool — semaphore dispatch, Run() + RunWithCallbacks()
    └── tui/
        ├── model.go         bubbletea Model — Init/Update/View
        ├── progress.go      Per-file progress bar helpers
        └── styles.go        Lipgloss theme constants
```

### Ownership rules

| Package | Owns | Does NOT own |
|---|---|---|
| `cmd` | CLI surface, flag/viper wiring | Any transcoding or scanning logic |
| `internal/config` | `Config` struct, viper deserialization | Flag definition, file I/O |
| `internal/scanner` | Directory walk, extension filtering | ffmpeg, workers, config |
| `internal/ffmpeg` | ffmpeg process lifecycle, progress parsing | Concurrency, file routing |
| `internal/worker` | Semaphore pool, job dispatch, result aggregation | ffmpeg args, TUI state |
| `internal/tui` | bubbletea model, render, keybindings | Transcoding logic, scanner |

---

## Data Flow

```
CLI flags / config.yaml / env vars
          │
          ▼
    config.Load()            internal/config
          │
          │ *Config
          ▼
    scanner.Scan()           internal/scanner
          │
          │ []MediaFile
          ▼
    worker.Pool.Run()        internal/worker
    ┌─────────────────────────────────────┐
    │  goroutine per file                 │
    │    semaphore.Acquire(1)             │
    │        │                           │
    │        ▼                           │
    │    ffmpeg.Run()        internal/ffmpeg
    │    ┌─────────────────────────────┐  │
    │    │ exec.Cmd (ffmpeg process)   │  │
    │    │ stderr → line scanner       │  │
    │    │ time= regex → ProgressEvent │  │
    │    │ onProgress(ev) callback     │  │
    │    └─────────────────────────────┘  │
    │    semaphore.Release(1)             │
    │    results ← Result{File, Err}      │
    └─────────────────────────────────────┘
          │
          │ chan Result
          ▼
    reporter (zerolog or TUI events)
```

### Step-by-step

1. **Config** — `config.Load()` reads viper's merged view of flags, env vars,
   and config.yaml into a `*Config` struct. All subsequent stages receive this
   struct; they never read viper directly.

2. **Scan** — `scanner.Scan(root, exts)` calls `doublestar.GlobWalk` on an
   `os.DirFS(root)` filesystem. It returns a `[]scanner.MediaFile` slice
   containing the absolute path, extension, and file size of every match.
   Scan is intentionally single-threaded and fast; it is not the bottleneck.

3. **Worker pool** — `worker.New(cfg)` constructs a `Pool` with a
   `semaphore.Weighted` of size `cfg.Workers`. `Pool.Run()` launches one
   goroutine per file. Each goroutine acquires one semaphore slot before
   calling `ffmpeg.Run()` and releases it after. This caps parallelism at
   exactly `cfg.Workers` in-flight processes.

4. **ffmpeg runner** — `ffmpeg.Run(ctx, src, dst, codec, onProgress)` builds
   the ffmpeg argument list, starts the process, and reads stderr line-by-line.
   Each line matching `time=HH:MM:SS.cs` emits a `ProgressEvent` via the
   `onProgress` callback. On process exit, `cmd.Wait()` returns; a non-zero
   exit produces an `*ExecError`; an OS-level failure (fork, pipe) produces an
   `*OSError`. The caller can distinguish these with a type switch.

5. **Result aggregation** — `Pool.Run()` collects results from a buffered
   channel. After all goroutines finish, it counts failures and returns a
   summary error if any files failed. `Pool.RunWithCallbacks()` (used by the
   TUI) fires `onProgress` and `onComplete` callbacks inline instead.

6. **Reporter** — In CLI mode the pool logs each event via `zerolog`. In TUI
   mode the callbacks send typed `tea.Msg` values to a buffered channel that
   the bubbletea event loop drains.

---

## Cancellation

Context cancellation propagates as follows:

```
cobra cmd.Context()       ← cancelled on OS signal (SIGINT/SIGTERM)
      │
      ▼
pool.Run(ctx, ...)
      │
      ▼
semaphore.Acquire(ctx, 1) ← returns ctx.Err() if cancelled while waiting
      │
      ▼
ffmpeg.Run(ctx, ...)
      │
      ▼
exec.CommandContext(ctx, "ffmpeg", ...)
      │                   ← ctx cancellation sends SIGKILL to ffmpeg child
      ▼
cmd.Wait()                ← returns *exec.ExitError with exit -1
      │
      ▼
*OSError{Err: ctx.Err()}  ← propagated back up the call stack
```

Key invariants:
- No goroutine blocks indefinitely after context cancellation.
- Partial output files (`.smelt` suffix) are cleaned up by the worker on any
  non-zero ffmpeg exit, including cancellation.
- The TUI passes `cmd.Context()` from the cobra command into `tui.New()`,
  ensuring the same cancellation signal reaches workers when the user presses
  `q`.

---

## Progress Event Flow

```
ffmpeg stderr (line by line)
      │
      │  "frame=  120 fps= 24 time=00:01:23.45 ..."
      ▼
runner.parseTime(line) → time.Duration
      │
      ▼
ProgressEvent{FilePath, Current, Total, Percent}
      │
      ▼
onProgress(ev) callback
      │
      ├─── CLI mode: zerolog.Debug().Float64("pct", ...).Msg("progress")
      │
      └─── TUI mode: events chan <- progressMsg{ev}
                           │
                           ▼
                    listenForEvent(events) → tea.Cmd
                           │
                           ▼
                    model.Update(progressMsg)
                           │
                           ▼
                    fileItems[i].prog.SetPercent(ev.Percent)
                           │
                           ▼
                    progress.Model.View() → rendered bar
```

`ffprobe` is run once per file at the start of transcoding to obtain the total
duration, which is used to compute `Percent`. If `ffprobe` fails (e.g., the
file has no duration metadata), `Percent` is `0` and the progress bar is
indeterminate.

---

## Error Types

`internal/ffmpeg` exposes two typed errors:

```go
// ExecError: ffmpeg ran but exited non-zero.
type ExecError struct {
    FilePath string
    ExitCode int
    Stderr   string  // last 200 bytes of stderr
}

// OSError: failure before or after ffmpeg runs (fork, pipe, context cancel).
type OSError struct {
    FilePath string
    Err      error   // wraps the original error; unwrappable
}
```

Callers distinguish them with a type switch:

```go
switch err := err.(type) {
case *ffmpeg.ExecError:
    log.Error().Int("exit_code", err.ExitCode).Msg("ffmpeg error")
case *ffmpeg.OSError:
    if errors.Is(err, context.Canceled) {
        // user cancelled
    }
}
```

---

## TUI Event Loop

```
tui.Model.Init()
    │
    ├── go pool.RunWithCallbacks(ctx, files, onProgress, onComplete)
    │       │ sends progressMsg / completeMsg / allDoneMsg → events chan
    │
    └── return listenForEvent(events)  ← first Cmd, blocks on channel read

tui.Model.Update(msg)
    │
    ├── progressMsg  → update fileItems[i].percent, SetPercent → animation Cmd
    │                  return listenForEvent(events)  ← re-subscribe
    │
    ├── completeMsg  → mark done/error, append log, update list item
    │                  return listenForEvent(events)
    │
    ├── allDoneMsg   → append summary log, stop re-subscribing
    │
    ├── progress.FrameMsg → tick all active progress.Model.Update()
    │
    └── tea.KeyMsg   → handle q/Q/p/j/k/?
```

The `listenForEvent` pattern is safe because the events channel is buffered
(capacity = `len(files)*4 + 4`), so worker goroutines never block on sends
even if the TUI event loop is momentarily busy.
