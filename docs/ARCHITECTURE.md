# smelt Architecture

End-to-end design of smelt: package ownership, data flow, cancellation, and
progress propagation.

---

## Package Map

```
smelt/
├── main.go                  Entry point — calls cmd.Execute()
├── cmd/                     Cobra command tree; no business logic
│   ├── root.go              Root command, persistent flags (incl. --db), openDB helper
│   ├── transcode.go         `smelt transcode` — delegates to scanner + worker
│   ├── tui.go               `smelt tui` — delegates to scanner + tui.Model
│   ├── check.go             `smelt check` — parallel ffprobe health check
│   ├── clean.go             `smelt clean` — remove orphaned .transcoded partial artifacts
│   ├── history.go           `smelt history` — query the SQLite history DB
│   ├── workflow.go          `smelt workflow` — emits a schedulable shell script
│   ├── config.go            `smelt config init` — writes starter config.yaml
│   └── version.go           `smelt version` — prints build metadata
└── internal/                All business logic; not importable outside the module
    ├── config/
    │   └── config.go        Config struct + Load() — reads viper state
    ├── scanner/
    │   ├── scanner.go       Scan() — filepath.WalkDir → []MediaFile (path/size/mtime/mode/links)
    │   └── links_{unix,other}.go  hardlink-count helper (build-tagged)
    ├── db/
    │   └── db.go            Open(WAL) — Insert/IsDone/Recent; Record struct
    ├── ffmpeg/
    │   ├── runner.go        Run(EncodeSpec) — exec wrapper, progress parsing, typed errors
    │   ├── accel.go         ResolveEncoder() — functional HW-encoder probe + fallback
    │   └── accel_{linux,darwin,windows}.go  per-GOOS hwPriority + vaapiDevice
    ├── worker/
    │   ├── pool.go          Pool — sequential dispatch, Run() + RunWithCallbacks(); DB recording
    │   └── gate.go          pausable dispatch gate (TogglePause/Paused)
    ├── workflow/
    │   └── workflow.go      Script() — render a transcode job as a shell script
    └── tui/
        ├── model.go         bubbletea Model — editable pre-flight, Init/Update/View
        ├── progress.go      Per-file progress bar helpers
        └── styles.go        Lipgloss theme constants
```

### Ownership rules

| Package | Owns | Does NOT own |
|---|---|---|
| `cmd` | CLI surface, flag/viper wiring, DB open/close | Any transcoding or scanning logic |
| `internal/config` | `Config` struct, viper deserialization | Flag definition, file I/O |
| `internal/scanner` | Directory walk, extension/permission/mtime collection | ffmpeg, workers, config |
| `internal/db` | SQLite WAL database, Record insert/query, DefaultPath | Business logic, config |
| `internal/ffmpeg` | ffmpeg/ffprobe process lifecycle, HW probe, progress parsing | Concurrency, file routing |
| `internal/worker` | Semaphore pool, job dispatch, DB recording, result aggregation | ffmpeg args, TUI state |
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

2. **Scan** — `scanner.Scan(root, exts)` uses `filepath.WalkDir` to walk the
   source tree. It returns a `[]scanner.MediaFile` slice containing the
   absolute path, relative path, extension, size, mtime, mode, and hardlink
   count of every match. Symlinked directories are never followed (prevents
   circular-symlink infinite walks, e.g. mise `trusted-configs`); symlinked
   files are resolved via `os.Stat` so ARR/seedbox setups work correctly.
   Per-entry errors (permissions, broken symlinks) are skipped silently; only
   a failure to access root itself is returned. Scan is single-threaded and
   fast; it is not the bottleneck.

3. **Worker pool** — `worker.New(cfg, db)` constructs a `Pool` with a
   `semaphore.Weighted` of size `cfg.Workers` and a pausable dispatch gate. The
   dispatch loop is **sequential**: for each file it waits at the gate (a no-op
   unless paused via the TUI's `p`), acquires one semaphore slot, then spawns a
   goroutine that runs `ffmpeg.Run()` and releases the slot. The semaphore caps
   parallelism at exactly `cfg.Workers`; pausing withholds *new* dispatch while
   in-flight jobs run on. The encoder is resolved once per run (`ResolveEncoder`,
   a functional GPU probe) and reused for every file. After each encode the result
   (success or failure) is written to the history DB via `pool.record()`.

4. **ffmpeg runner** — `ffmpeg.Run(ctx, src, dst, spec, onProgress)` takes a
   resolved `EncodeSpec` (codec, crf, preset, audio, container, resolved
   encoder/backend, passthrough args), builds the argument list, starts the
   process, and reads stderr line-by-line.
   Each line matching `time=HH:MM:SS.cs` emits a `ProgressEvent` via the
   `onProgress` callback. On process exit, `cmd.Wait()` returns; a non-zero
   exit produces an `*ExecError`; an OS-level failure (fork, pipe) produces an
   `*OSError`. The caller can distinguish these with a type switch.

5. **Result aggregation** — `Pool.Run()` (the CLI path) is a thin wrapper over
   `Pool.RunWithCallbacks()`: it passes zerolog callbacks, tallies failures with
   an atomic counter, and returns a summary error if any files failed. The TUI
   passes callbacks that forward typed `tea.Msg` values to its event channel.
   Both share the one dispatch loop.

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
- The transient working file (`<name>.transcoded<ext>`) is deleted by the worker
  on any non-zero ffmpeg exit, including cancellation; it never survives a run.
  The final `.smelt` output only exists after a successful rename.
- The TUI derives a cancellable child of `cmd.Context()` and calls its cancel
  func on `q`/`Q`, so an in-app quit kills in-flight ffmpeg even without an OS
  signal; on `q` it then waits for the pool to drain before exiting.

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
    └── return resolveCmd()       ← probes the encoder for the pre-flight screen
                                    (the pool is NOT started yet)

  ── editable pre-flight screen ──
    user edits codec/crf/preset/hwaccel/workers, then presses enter/s
    │
    └── pool = worker.New(cfg);  go pool.RunWithCallbacks(...)
            sends progressMsg / completeMsg / allDoneMsg → events chan
        return listenForEvent(events)  ← begin draining the channel

tui.Model.Update(msg)
    │
    ├── resolvedMsg  → record the concrete encoder for the hwaccel row
    │
    ├── progressMsg  → update fileItems[i].percent, SetPercent → animation Cmd
    │                  return listenForEvent(events)  ← re-subscribe
    │
    ├── completeMsg  → mark done/error/cancelled, append log, update list item
    │                  return listenForEvent(events)
    │
    ├── allDoneMsg   → mark finished (or quit, if cancelling)
    │
    ├── progress.FrameMsg → tick all active progress.Model.Update()
    │
    └── tea.KeyMsg   → pre-flight: edit/start;  running: q/Q/p/j/k/?
```

The `listenForEvent` pattern is safe because the events channel is buffered
(capacity = `len(files)*4 + 4`), so worker goroutines never block on sends
even if the TUI event loop is momentarily busy.
