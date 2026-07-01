# Design Discussion — Surgical Mode & the Simple/Power Tension

> Status: **discussion / scratchpad.** Nothing here is scheduled or committed.
> This captures an ongoing design conversation so we can keep throwing ideas at
> it before any implementation work starts. Decisions recorded here are
> *intent*, not code — see "Help Driven Development" below for why that matters.

## The problem

smelt serves two audiences with opposite wants:

- **Homelabbers** who want it to *just work* — auto-detected hardware, sane
  defaults, one command, zero ffmpeg knowledge. If they happen to own one Dolby
  Vision movie, the tool must not silently wreck it.
- **Power users** who want to reach into the nitty-gritty — DV/HDR pipelines,
  raw `-x265-params`, per-stream decisions.

Naively this looks like "ship a simple mode and an advanced mode." That's a
trap: it forces every user to self-classify up front and bifurcates the
codebase. The homelabber who picked "simple" is the one most likely to silently
degrade a complex file.

## The frame: progressive disclosure, not two modes

One path, with layers you opt into only as deep as you care. Most of this
already exists in the code — it just wasn't named as a philosophy.

| Layer | What | Status today |
|---|---|---|
| 0 | Zero config — auto-resolve encoder/backend, sane defaults | ✅ `ResolveEncoder`, config defaults |
| 1 | Named knobs — `--codec/--crf/--preset/--hwaccel`, cross-encoder *normalized* | ✅ `encoderPreset` maps x264 names onto nvenc/svt/etc. |
| 2 | **Profiles** — expert-authored named bundles | ⚠️ profiles merge into config, but need a small policy vocabulary (below) |
| 3 | Raw escape hatch — `--ffmpeg-arg` / `extra_args` | ✅ `EncodeSpec.ExtraArgs` |
| 4 | **Surgical / source-aware** — multi-stage DV/HDR pipelines | ❌ not yet |

Layers 2 and 4 are the missing pieces, and together they answer the whole
question.

## Guiding philosophies

### Progressive disclosure
Homelabbers live at Layer 0 with auto-detection; power users author profiles and
reach Layers 3–4; neither is forced into the other's world.

### HDD — Hard Disk Drive
smelt's home turf is homelab NAS boxes on spinning rust. The surgical pipeline
is where this bites hardest, because multi-pass DV work can materialize
tens-of-GB intermediates. Two hard rules fall out:

1. **Pipe stages, don't materialize, wherever the toolchain allows.** The
   reference DV flow already streams the RPU extract through stdout
   (`ffmpeg … -f hevc - | dovi_tool extract-rpu -`) and never touches disk for
   it. Materialize *only* what genuinely must be a file (the encoded elementary
   stream that `inject-rpu` reads, the RPU.bin). Everything else stays piped.
2. **Surgical jobs run at reduced concurrency.** The normal pool runs N parallel
   ffmpeg. N parallel *multi-GB-intermediate* jobs on one HDD = N× seek thrash
   and N× scratch footprint. Surgical jobs likely serialize (or use a separate,
   smaller semaphore) independent of the fast-path worker count.

Plus the mundane-but-critical: intermediates honor a configurable temp dir
(ideally on the *output* volume so the final step is a rename, not a cross-device
copy), and every transient is cleaned on any failure — the existing
transient-file discipline generalizes straight into this.

### HDD — Help Driven Development
The `--help` output and `docs/CLI.md` are a **contract** with the user; they must
never drift from actual behavior. This is a standing rule but especially load-
bearing for DV/HDR, which adds new flags, new detection warnings, and new failure
modes:

- When surgical mode ships, `docs/CLI.md` **and** every affected `--help` string
  are updated *in the same change* as the code — never after.
- The help-menu *look* is deliberately curated, not auto-dumped: grouped,
  scannable, homelabber-legible. New surgical flags slot into that curation, they
  don't bolt on at the end.
- Detection warnings the user sees at scan time are themselves "help" and get the
  same care.

## Key idea #1 — Profiles are the bridge between the two audiences

The power user's knowledge (`-x265-params hdr-opt=1:...`, DV profile choice,
master-display string) shouldn't live in flags the homelabber has to type. It
lives in a **named profile the expert authors once**:

- Power user writes `profiles.dv-archive` with all the params.
- Homelabber types `--profile dv-archive` — or types nothing, because smelt
  **auto-selects it from detection** (idea #3).

So the simple/power divide stops being a *mode the user picks* and becomes
*content experts author and everyone consumes*. smelt can **ship curated
profiles** (`dv-preserve`, `anime-hevc`, `archive-av1`) so the expertise is
baked in — "just use the file I ship."

### A profile *selects* a pipeline, it can't *be* one

Today a profile is a flat bag of parameters that merges into one `EncodeSpec`
→ one `ffmpeg.Run`. The DV pipeline is **not** one ffmpeg call — it's
extract RPU → encode → inject → remux across `ffmpeg` *and* `dovi_tool`. You
cannot express "run four tools in sequence" as `extra_args` on a single
invocation.

Resolution: give the profile schema a small vocabulary of **policy fields** (not
raw args) that the planner reads to choose the execution shape. `pipeline:` was
rejected — it collides with the `smelt workflow` rule concept and muddies two
unrelated ideas. Favoured shape is an intent-named `preserve:` list:

```yaml
profiles:
  dv-archive:
    codec: h265
    crf: 18
    preset: slow
    preserve: [dv, hdr]   # ← policy/intent, not ffmpeg args; switches the
                          #    planner to the multi-stage pipeline
```

Division of labor: **the profile carries intent + parameters; the code carries
execution.** `preserve` is what turns a one-stage pipeline into a multi-stage
one. The homelabber never sees the stages — they just name the profile (or it's
auto-applied). Ergonomics stay "ship the file, use the file."

## Key idea #2 — Detect always, let policy decide

Separate **detection** from **action**:

- **Detection is cheap and always on.** The scan phase already probes every file
  (`ProbeAttrs`/`ProbeHealth`), so DV-RPU / HDR-metadata detection piggybacks on
  a walk we already do — no extra pass.
- **Warn at scan time, before any transcode starts**, loud and clear, on **both**
  the TUI and the CLI. This is the seam where the user learns "3 of these files
  are Dolby Vision, here's what I'm going to do."
- **A policy decides the action** when complexity is found:
  - `auto` (default): preserve if the tools are available; if the required tool
    is missing, warn loudly. **Default routing on detected DV/HDR is to
    auto-handle** (correctness-by-default; the scan-time warning defuses the
    surprise).
  - `strict`: fail hard if it can't preserve.
  - `fast`: ignore the complexity, just transcode.

## Key idea #3 — Auto-select a profile from detection

The "auto resolution" dream: detection can pick a whole **profile**, not just a
policy. Detect DV → auto-apply the shipped `dv-preserve` profile. The homelabber
types nothing and gets expert handling. Explicit flags/`--profile` always
override (see precedence).

## Architectural throughline — Pipeline of stages

Today a job is *one* `ffmpeg.Run` producing one `EncodeSpec`. The DV path is
inherently multi-stage. So generalize:

> An encode is a **Pipeline of stages**, where the simple case is a one-stage
> pipeline.

The simple path does not get more complex — still one stage. But the executor,
progress plumbing, and cancellation/cleanup all become uniform:

- **`Plan`** already "resolves" skip-logic and output paths. Extend "resolution"
  to also resolve *source complexity → pipeline shape*. Natural extension of an
  existing concept, not a bolt-on.
- **Cleanup**: multi-stage means transient artifacts (RPU.bin, elementary
  streams). The existing transient-file discipline generalizes to per-stage
  scratch that's cleaned on any failure.
- **Progress**: see the resolved decision below — we do **not** try to synthesize
  a cross-stage ETA.

## Decisions (resolved in discussion)

- **Default behavior on detected DV/HDR:** auto-route to the surgical pipeline
  (auto-handle), with a loud scan-phase warning on both TUI and CLI. Not opt-in.
- **Scope includes HDR, not just DV.** HDR10 *static* metadata
  (master-display / max-CLL) rides along as `-x265-params` passthrough on the
  normal encode — cheap. DV needs `dovi_tool`; HDR10+ *dynamic* needs its own
  external tool (`hdr10plus_tool`). Whatever the tool, the UX is the same (next
  point).
- **External tools are optional and lazily gated.** `CheckDeps` still hard-
  requires ffmpeg/ffprobe at startup. The surgical tools are checked **only when
  the matching complexity is actually detected** — a non-DV/HDR user is never
  forced to install them. If the tool is missing, smelt says plainly *"can't
  handle this surgically without `<tool>`; install it, or re-run with `--fast`."*
- **In-place safety: verify → replace, reject on failure.** For surgical +
  in-place, run a verification pass (e.g. `dovi_tool info` confirms the RPU
  survived) **before** deleting/replacing the source. If verification fails, do
  **not** replace — keep the original untouched and surface the error/warning.
  Never destroy the only good copy on a botched-but-"successful" inject.
- **DV profile target: normalize to 8.1.** Assuming the conversion is reversible,
  target single-layer profile 8.1 for the widest player compatibility.
- **Container tag mismatch: warn, auto-correct, proceed.** If a DV source is
  muxed to a container needing a specific tag (e.g. `dvh1`/`hvc1` for MP4) and the
  user didn't set it, it's almost certainly just a forgotten tag — apply the
  corrected tag automatically, warn, and continue rather than refusing.
- **Progress/ETA across stages: don't synthesize it.** ffmpeg (and dovi_tool)
  report live progress per stage. As long as the timer is ticking and the active
  tool is emitting progress, the user knows it's working — that's sufficient.
  Guessing a blended cross-stage ETA by weighting stages **invites errors**; we
  won't do it. A bar that resets per stage / a brief "90% then next stage" is an
  accepted **minor** UX cost, not a problem to engineer around.

## Precedence (resolved)

Single user-facing command. Explicit user input always wins over shipped
intelligence:

```
CLI flag  >  explicit --profile  >  auto-selected profile (from detection)  >  config.yaml  >  built-in default
```

## Open questions still worth chewing on

- **Policy-key naming.** `preserve: [dv, hdr]` is the current favourite (intent-
  named, avoids the `workflow`/`pipeline` collision). Alternative: a per-feature
  scalar (`dv: preserve|fast|strict`). Not locked.
- **HDR10+ dynamic scope for v1.** Static HDR10 is nearly free (x265-params). Is
  HDR10+ dynamic (needs `hdr10plus_tool`) in the first cut, or a fast-follow?
- **Surgical concurrency knob.** Serialize surgical jobs entirely, or expose a
  separate small `--surgical-workers`-style limit distinct from the fast-path
  worker count?
- **Temp/scratch location policy.** Default to the output volume for
  rename-not-copy finalization; honor `$TMPDIR`/a config key; how to behave when
  the output volume lacks space for a big elementary stream.
- **Curated profiles shipped in-box on day one.** Which ones, and their exact
  params.

## Related follow-ups (outside this doc's scope but noted)

- **Docker image** was `FROM scratch` (no ffmpeg) — fixed to Alpine + `apk add
  ffmpeg`. When surgical ships, decide whether the official image also bundles
  `dovi_tool`/`hdr10plus_tool` or documents them as bring-your-own.
- **Help Driven Development debt:** every item above that becomes real code must
  land with its `docs/CLI.md` + `--help` update in the same change.
