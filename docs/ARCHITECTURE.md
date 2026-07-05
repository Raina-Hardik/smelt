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
│   ├── watch.go             `smelt watch` — polls --src on a timer, reusing scanner+worker.Plan each pass
│   ├── each.go              `smelt each` — scans + emits one job row + one path per line (script primitive)
│   ├── match.go             `smelt match` — evaluates one Cond against a file, exit 0/1 (script primitive)
│   ├── do.go                `smelt do` — applies transcode/check to a single file, DB-tracked (script primitive)
│   ├── finishrun.go         `smelt finish-run` — reconciles + closes a tracked run (script primitive)
│   ├── serve.go             `smelt serve` — starts the HTTP API server (internal/server)
│   ├── config.go            `smelt config init` — writes starter config.yaml
│   └── version.go           `smelt version` — prints build metadata
└── internal/                All business logic; not importable outside the module
    ├── config/
    │   └── config.go        Config struct + Load() — reads viper state
    ├── scanner/
    │   ├── scanner.go       Scan() — filepath.WalkDir → []MediaFile (path/size/mtime/mode/links)
    │   └── links_{unix,other}.go  hardlink-count helper (build-tagged)
    ├── db/
    │   └── db.go            Open(WAL) — full state plane: transcodes, runs, jobs, programs tables + query API
    ├── ffmpeg/
    │   ├── runner.go        Run(EncodeSpec) — exec wrapper, progress parsing, typed errors
    │   ├── accel.go         ResolveEncoder() — functional HW-encoder probe + fallback
    │   └── accel_{linux,darwin,windows}.go  per-GOOS hwPriority + vaapiDevice
    ├── worker/
    │   ├── pool.go          Pool — sequential dispatch, Run() + RunWithCallbacks() + RunTracked(); DB recording
    │   └── gate.go          pausable dispatch gate (TogglePause/Paused)
    ├── workflow/
    │   ├── workflow.go      Script() — render a single `smelt transcode` invocation as a shell script
    │   └── program.go       Program/Rule/Cond/Action IR + Render()/Parse()/ParseRule() — per-file decision pipelines
    ├── server/
    │   ├── server.go        Server — http.ServeMux wiring, Start(addr)
    │   ├── handlers.go      Program/run CRUD + trigger/cancel HTTP handlers, JSON (de)serialization
    │   ├── exec.go          triggerRun() — renders a Program to a script, execs it as a tracked subprocess
    │   └── exec_{unix,windows}.go  per-GOOS process-group setup for clean cancellation (SIGTERM the group on unix)
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
| `internal/db` | SQLite WAL database, Record insert/query, DefaultPath, runs/jobs/programs tables | Business logic, config |
| `internal/ffmpeg` | ffmpeg/ffprobe process lifecycle, HW probe, progress parsing | Concurrency, file routing |
| `internal/worker` | Semaphore pool, job dispatch, DB recording, result aggregation | ffmpeg args, TUI state |
| `internal/workflow` | Program IR, shell-script rendering/parsing | HTTP, process execution |
| `internal/server` | HTTP routing, program storage via `internal/db`, subprocess lifecycle | Transcoding logic, script IR |
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
    └── return tea.Batch(resolveCmd(), scanCmd())
            resolveCmd  ← probes the encoder off-thread; sends resolvedMsg
            scanCmd     ← scanner.Scan + worker.Plan off-thread; sends scanDoneMsg

  ── scanning state (TUI is open, scan running in background) ──
    scanDoneMsg arrives → file list built; pre-flight screen shown
    (or scanDoneMsg.err → error screen shown, only q works)

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

---

## HTTP API (`smelt serve`)

`smelt serve` (`internal/server`) exposes the SQLite history DB and the
per-file decision engine (`internal/workflow`'s `Program` IR) over HTTP for
the dashboard WebUI. There is no separate execution engine behind the API —
every run is the same "render a script, exec it as a subprocess" model
`smelt workflow` uses for cron, just triggered over HTTP instead of a
crontab line:

```
POST /api/programs/{id}/run
      │
      ▼
handlers.go: handleRunProgram
      │  load Program{} from DB (db.GetProgram)
      │  generate run_id (uuid)
      ▼
exec.go: triggerRun(runID, program)
      │
      │  workflow.Render(program, Options{Binary: <this executable>,
      │                             DBSet: true, DBPath: <server's --db>})
      │      ── every each/do/finish-run invocation in the rendered
      │         script gets an explicit --db flag equal to the path the
      │         server itself opened; without this the subprocess falls
      │         back to db.DefaultPath() and the server would trigger
      │         runs it can never see through its own /api/runs.
      ▼
  write script to <scripts-dir>/smelt-<run_id>.sh
  exec.Command("sh", scriptPath), SMELT_RUN_ID=<run_id>, stdout/stderr → <script>.log
      │  s.procs.Store(runID, cmd.Process)   ← tracked for cancellation
      ▼
  script runs: smelt each | while … smelt match … smelt do … done; smelt finish-run
      │  each spawned smelt subcommand uses the inherited --db to write
      │  runs/jobs rows the server's own DB handle can read back
      ▼
  GET /api/runs/{id}  (polled by the dashboard every ~2s while running)
      reads the same runs/jobs rows via internal/db, independent of whether
      the subprocess is still alive

  DELETE /api/runs/{id}
      looks up s.procs[runID] and sends SIGTERM to the process group
      (setSysProcAttr / exec_unix.go), so cancelling the parent `sh` also
      kills any in-flight ffmpeg child; 409 if no live process is tracked
      (already finished, or the server restarted and lost the in-memory map)
```

Programs themselves (`POST/GET/PUT/DELETE /api/programs`) are plain CRUD
against `internal/db`'s `programs` table — `workflow.Rule`/`Cond`/`Action`
carry `json` tags matching the wire format documented in `docs/CLI.md`'s API
reference (`when`/`do`/`field`/`op`/`value`/`cmd`/`args`), so a program
fetched from the API round-trips through the same JSON shape it was created
with. `POST`/`PUT` run each rule's conditions through `workflow.ValidateRule`
and reject any field/operator combination `smelt match` has no flag for with
`400` — the same check `smelt workflow --rule` applies at render time (see
`docs/WORKFLOW.md`'s operator compatibility table), so an invalid rule can't
reach the database via either path.
