# smelt Configuration Reference

smelt uses a single YAML config file. Run `smelt config init` to generate a
fully annotated starter.

---

## File Discovery

smelt searches for a config file in this order:

1. Path given by `--config` (if provided)
2. `./config.yaml` (current working directory)
3. Platform config directory:
   - **Linux** — `$XDG_CONFIG_HOME/smelt/config.yaml` (default `~/.config/smelt/config.yaml`)
   - **macOS** — `~/Library/Application Support/smelt/config.yaml`
   - **Windows** — `%AppData%\smelt\config.yaml`

If no file is found, built-in defaults apply. No error is raised.

---

## Schema

```yaml
smelt:
  workers: 4                  # int    — parallel ffmpeg jobs; 0 = runtime.NumCPU()
  log_level: info             # string — debug | info | warn | error
  log_format: auto            # string — auto | json | pretty
  db: ""                      # string — SQLite history DB path; "" = $XDG_DATA_HOME/smelt/history.db

transcode:
  src: ""                     # string   — source directory path (required)
  ext: [mkv, mp4, avi]        # []string — extensions to match, no leading dot
  codec: h265                 # string   — h264 | h265 | av1 | vp9
  crf: 23                     # int      — constant rate factor, 0–51
  preset: medium              # string   — encoding preset (normalized per encoder)
  hwaccel: auto               # string   — auto | none | nvenc | qsv | vaapi | amf | videotoolbox
  hwdecode: auto              # string   — auto | off; hw decode on the encoder's device when probed decodable
  audio_codec: copy           # string   — copy | aac | opus | mp3 | ac3 | flac
  audio_bitrate: ""           # string   — e.g. 192k; only when re-encoding audio
  subs: copy                  # string   — copy | drop (subtitle stream handling)
  inplace: false              # bool     — replace original after successful transcode
  skip_hardlinked: false      # bool     — with inplace, skip hardlinked files
  skip_source_codecs: []      # []string — skip files already in these codecs (e.g. [av1])
  force: false                # bool     — re-transcode even when already up to date
  to: ""                      # string   — target container (mp4|mkv|webm|…); empty keeps source
  output_dir: ""              # string   — redirect output here; empty = alongside source
  suffix: ".smelt"            # string   — output filename suffix (alongside source)
  decode_threads: 0           # int      — cap decoder thread count; 0 = ffmpeg default
  allow_hdr_loss: false       # bool     — required to transcode a detected Dolby Vision source

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

---

## Key Reference

### `smelt` section

Controls global runtime behaviour.

#### `smelt.workers`

| Attribute | Value |
|---|---|
| Type | `int` |
| Default | `0` (resolved to `runtime.NumCPU()` at startup) |
| CLI flag | `--workers` |
| Env var | `SMELT_WORKERS` |

Maximum number of ffmpeg processes that may run simultaneously. A value of `0`
automatically uses all logical CPU cores. Set a lower value on machines shared
with other workloads.

```yaml
smelt:
  workers: 4
```

#### `smelt.log_level`

| Attribute | Value |
|---|---|
| Type | `string` |
| Default | `info` |
| Valid values | `debug` \| `info` \| `warn` \| `error` |
| CLI flag | `--log-level` |
| Env var | `SMELT_LOG_LEVEL` |

Controls log verbosity. `debug` emits per-file progress on every ffmpeg stderr
line; `error` shows only failures.

```yaml
smelt:
  log_level: debug
```

#### `smelt.log_format`

| Attribute | Value |
|---|---|
| Type | `string` |
| Default | `auto` |
| Valid values | `auto` \| `json` \| `pretty` |
| CLI flag | `--log-format` |
| Env var | `SMELT_LOG_FORMAT` |

`auto` selects `pretty` (colorized, human-readable) when stderr is a TTY, and
`json` (newline-delimited JSON objects) otherwise. Logs are written to stderr;
use `json` explicitly when piping to log aggregators.

```yaml
smelt:
  log_format: json
```

#### `smelt.db`

| Attribute | Value |
|---|---|
| Type | `string` |
| Default | Platform data directory — see below |
| CLI flag | `--db` |
| Env var | `SMELT_DB` |

Path to the SQLite history database. Every completed transcode (success or
failure) is recorded here with timestamps, encoder settings, elapsed time, and
file sizes. Used by `smelt history` and for fast skip detection on `--inplace`
re-runs (avoids re-probing files whose mtime hasn't changed). Set to `""` to
disable history recording entirely.

Default path per platform (respects `$XDG_DATA_HOME` on all platforms):

| Platform | Default path |
|---|---|
| Linux | `$XDG_DATA_HOME/smelt/history.db` → `~/.local/share/smelt/history.db` |
| macOS | `~/Library/Application Support/smelt/history.db` |
| Windows | `%LocalAppData%\smelt\history.db` |

```yaml
smelt:
  db: /mnt/media/.smelt-history.db
```

---

### `transcode` section

Controls how files are discovered and how ffmpeg is invoked.

#### `transcode.src`

| Attribute | Value |
|---|---|
| Type | `string` |
| Default | `.` (current directory) |
| CLI flag | `--src` |
| Env var | `SMELT_SRC` |

Root directory for the recursive media scan. All subdirectories are walked.
Symlinks are not followed.

```yaml
transcode:
  src: /mnt/media/library
```

#### `transcode.ext`

| Attribute | Value |
|---|---|
| Type | `[]string` |
| Default | `[mkv, mp4, avi]` |
| CLI flag | `--ext` |
| Env var | `SMELT_EXT` |

File extensions to include in the scan. Case-insensitive. Leading dots are
stripped automatically, so `mkv` and `.mkv` are equivalent. `SMELT_EXT` (a flat
env var, unlike the YAML list) takes a comma-separated string, e.g.
`SMELT_EXT=mkv,mp4,avi`.

```yaml
transcode:
  ext: [mkv, mp4, avi, mov, wmv]
```

#### `transcode.codec`

| Attribute | Value |
|---|---|
| Type | `string` |
| Default | `h265` |
| Valid values | `h264` \| `h265` \| `av1` \| `vp9` |
| CLI flag | `--codec` |
| Env var | `SMELT_CODEC` |

Target video codec. smelt maps these aliases to the correct ffmpeg encoder:

| Alias | ffmpeg encoder |
|---|---|
| `h264` / `avc` | `libx264` |
| `h265` / `hevc` | `libx265` |
| `av1` | `libsvtav1` |
| `vp9` | `libvpx-vp9` |

Any other value is passed directly to ffmpeg's `-c:v` argument.

```yaml
transcode:
  codec: h265
```

#### `transcode.crf`

| Attribute | Value |
|---|---|
| Type | `int` |
| Default | `23` |
| Range | `0–51` |
| CLI flag | `--crf` |
| Env var | `SMELT_CRF` |

Constant Rate Factor. Controls the trade-off between quality and file size.
Lower values produce larger, higher-quality files. Typical ranges:

| Quality | CRF range |
|---|---|
| Lossless | `0` |
| High | `18–22` |
| Good (default) | `23–28` |
| Acceptable | `29–35` |
| Small/low | `36–51` |

```yaml
transcode:
  crf: 18
```

#### `transcode.preset`

| Attribute | Value |
|---|---|
| Type | `string` |
| Default | `medium` |
| CLI flag | `--preset` |
| Env var | `SMELT_PRESET` |

Encoding preset (speed vs. size at a given CRF). x264/x265 presets, fastest to
slowest: `ultrafast`, `superfast`, `veryfast`, `faster`, `fast`, `medium`,
`slow`, `slower`, `veryslow`. The value is **normalized into the resolved
encoder's namespace** so it never breaks a hardware encode: NVENC → `fast`/
`medium`/`slow` (or pass `p1`–`p7`), QSV → `veryfast`…`veryslow`, SVT-AV1 → a
number `0`–`13`. Ignored for VP9/VAAPI/AMF/VideoToolbox.

```yaml
transcode:
  preset: slow
```

#### `transcode.hwaccel`

| Attribute | Value |
|---|---|
| Type | `string` |
| Default | `auto` |
| Valid values | `auto` \| `none` \| `nvenc` \| `qsv` \| `vaapi` \| `amf` \| `videotoolbox` |
| CLI flag | `--hwaccel` |
| Env var | `SMELT_HWACCEL` |

Hardware-accelerated encoding. `auto` *functionally probes* for a usable GPU
encoder for the target codec (running a tiny test encode — compiled-in is not
enough) and falls back to software; `none` forces software. An explicit backend
that turns out unusable also falls back. The chosen encoder is logged at start.

When a hardware encoder resolves on `nvenc`, `qsv`, or `vaapi`, decode also
runs on that *same device* for every file a per-file probe confirms is
hardware-decodable there (see `transcode.hwdecode`), keeping the whole video
pipeline in device memory. Files that aren't hardware-decodable — or all
files with `hwdecode: off` — decode in software, and smelt logs a
resource-profile warning for that combination: full-CPU decode running
concurrently with the GPU/QSV/NVENC encode block is the most thermally
demanding split on constrained hardware (this also fires on `--dry-run`,
before any file is touched). Use `decode_threads` to cap the software-decode
side, and see
[Running politely on constrained hardware](../README.md#running-politely-on-constrained-hardware)
for OS-level throttling. `amf` and `videotoolbox` are encode-only backends:
decode is always software there this release.

```yaml
transcode:
  hwaccel: auto
```

#### `transcode.hwdecode`

| Attribute | Value |
|---|---|
| Type | `string` |
| Default | `auto` |
| Valid values | `auto` \| `off` |
| CLI flag | `--hwdecode` |
| Env var | `SMELT_HWDECODE` |

Hardware-accelerated decoding, on the *same device* as the resolved hardware
encoder — never a second GPU; frames never cross a device boundary. `auto`
decodes a file in hardware when a functional probe against the actual file
confirms the device can decode it; the result is cached per source shape
(codec × profile × pixel format), so identically-shaped libraries pay for one
~1 s probe. A file whose shape the device can't decode falls back to software
decode silently (debug-logged once per shape). No value *forces* hardware
decode; `off` disables it entirely.

If ffmpeg fails while hardware decode is active, the file is retried exactly
once with software decode before being counted as failed, and the cached probe
result for that shape is evicted. 10-bit sources use the hardware pipeline
only when the device can also encode `main10` (probed); otherwise the whole
file takes the software pipeline rather than being silently flattened to
8-bit. Has no effect with a software encoder, or on the encode-only
`amf`/`videotoolbox` backends.

```yaml
transcode:
  hwdecode: auto
```

#### `transcode.audio_codec`

| Attribute | Value |
|---|---|
| Type | `string` |
| Default | `copy` |
| Valid values | `copy` \| `aac` \| `opus` \| `mp3` \| `ac3` \| `flac` |
| CLI flag | `--audio-codec` |
| Env var | `SMELT_AUDIO_CODEC` |

`copy` stream-copies the audio untouched (no re-encode). Any other value
re-encodes (`opus` → `libopus`, `mp3` → `libmp3lame`, etc.).

```yaml
transcode:
  audio_codec: copy
```

#### `transcode.audio_bitrate`

| Attribute | Value |
|---|---|
| Type | `string` |
| Default | `""` (encoder default) |
| CLI flag | `--audio-bitrate` |
| Env var | `SMELT_AUDIO_BITRATE` |

Target audio bitrate when re-encoding, e.g. `192k`. Ignored when
`audio_codec: copy`.

```yaml
transcode:
  audio_bitrate: 192k
```

#### `transcode.subs`

| Attribute | Value |
|---|---|
| Type | `string` |
| Default | `copy` |
| Valid values | `copy` \| `drop` |
| CLI flag | `--subs` |
| Env var | `SMELT_SUBS` |

Subtitle stream handling. `copy` preserves all subtitle tracks from the source
(the default, so embedded subtitles survive transcoding). `drop` strips all
subtitle streams from the output (equivalent to `-sn` in ffmpeg). Note: when
using `--to mp4`, some subtitle codecs (e.g. PGS, ASS) are not supported by the
MP4 muxer — use `subs: drop` to avoid mux errors.

```yaml
transcode:
  subs: copy
```

#### `transcode.skip_source_codecs`

| Attribute | Value |
|---|---|
| Type | `[]string` |
| Default | `[]` (skip nothing) |
| CLI flag | `--skip-source-codec` (repeatable) |
| Env var | `SMELT_SKIP_SOURCE_CODEC` |

Skip files whose current video codec matches any entry in this list. Accepts the
same aliases as `transcode.codec` (`h264`, `h265`, `av1`, `vp9`) as well as raw
ffprobe codec names (`hevc`, `h264`, `av1`). Useful for protecting
already-optimal files from being downgraded. `SMELT_SKIP_SOURCE_CODEC` (a flat
env var, unlike the YAML list) takes a comma-separated string, e.g.
`SMELT_SKIP_SOURCE_CODEC=av1,h265`.

```yaml
transcode:
  skip_source_codecs: [av1]    # never re-encode files that are already AV1
```

#### `transcode.inplace`

| Attribute | Value |
|---|---|
| Type | `bool` |
| Default | `false` |
| CLI flag | `--inplace` |
| Env var | `SMELT_INPLACE` |

When `true`, atomically replaces the original file with the transcoded output
after a confirmed successful ffmpeg exit. The original is **unrecoverable**
after this operation. Consider combining with a `--dry-run` pass first.

Files **already in the target codec are skipped** (probed with `ffprobe`),
making in-place runs idempotent. Override with `force: true`.

```yaml
transcode:
  inplace: false
```

#### `transcode.skip_hardlinked`

| Attribute | Value |
|---|---|
| Type | `bool` |
| Default | `false` |
| CLI flag | `--skip-hardlinked` |
| Env var | `SMELT_SKIP_HARDLINKED` |

With `inplace: true`, skip files whose hardlink count is greater than one.
Transcoding replaces the inode, which breaks the hardlink (e.g. to a torrent
client's copy) and doubles disk usage — useful for ARR/seedbox setups. No effect
without `inplace`. Overridden by `force`.

```yaml
transcode:
  skip_hardlinked: true
```

#### `transcode.force`

| Attribute | Value |
|---|---|
| Type | `bool` |
| Default | `false` |
| CLI flag | `--force` |
| Env var | `SMELT_FORCE` |

Re-transcode even when a file is already up to date. Disables all skipping:
normal runs otherwise skip existing outputs (and smelt's own `<suffix>` files);
in-place runs otherwise skip files already in the target codec.

```yaml
transcode:
  force: false
```

#### `transcode.to`

| Attribute | Value |
|---|---|
| Type | `string` |
| Default | `""` (keep source container) |
| CLI flag | `--to` |
| Env var | `SMELT_TO` |

Target output container/format, e.g. `mp4`, `mkv`, `webm`. Changes only the
container (extension/muxer), not the codec. For mp4 it adds `+faststart` and tags
HEVC as `hvc1`. Mutually exclusive with `inplace`.

```yaml
transcode:
  to: mp4
```

#### `transcode.output_dir`

| Attribute | Value |
|---|---|
| Type | `string` |
| Default | `""` (alongside source) |
| CLI flag | `--output-dir` |
| Env var | `SMELT_OUTPUT_DIR` |

When set, all output files are written into this directory, preserving the
relative path structure from `transcode.src`. The directory must already exist.
Mutually exclusive with `inplace: true`.

```yaml
transcode:
  output_dir: /mnt/transcoded
```

#### `transcode.suffix`

| Attribute | Value |
|---|---|
| Type | `string` |
| Default | `.smelt` |
| CLI flag | `--suffix` |
| Env var | `SMELT_SUFFIX` |

Filename suffix for outputs written alongside the source (`<name><suffix><ext>`),
e.g. `movie.smelt.mkv`. Ignored for `inplace` and `output_dir`. (During encoding
ffmpeg writes to a transient `<name>.transcoded<ext>`, which is renamed to the
final name on success and deleted on any failure.)

```yaml
transcode:
  suffix: .smelt
```

#### `transcode.decode_threads`

| Attribute | Value |
|---|---|
| Type | `int` |
| Default | `0` (ffmpeg default) |
| CLI flag | `--decode-threads` |
| Env var | `SMELT_DECODE_THREADS` |

Caps ffmpeg's decoder thread count. Emitted as a global `-threads N` *before*
`-i`, so it constrains decode specifically — `extra_args`/`--ffmpeg-arg`
options land after `-i` and only affect the encoder. Useful for large
software-decoded sources (e.g. 10-bit 4K HEVC) run alongside a hardware
encoder backend, where decode is otherwise uncapped. While hardware decode is
active for a file (see `transcode.hwdecode`), `-threads` is omitted from the
command; the cap still applies on every software-decode path (opt-out,
un-decodable sources, and the automatic retry). See the `hwaccel`
resource-profile warning above.

```yaml
transcode:
  decode_threads: 4
```

#### `transcode.ffmpeg_args`

| Attribute | Value |
|---|---|
| Type | `[]string` |
| Default | `[]` |
| CLI flag | `--ffmpeg-arg` (repeatable) |
| Env var | none |

Raw ffmpeg arguments passed through verbatim, appended after all computed
flags (so they can override them). Combines with a profile's own `extra_args`:
profile args come first, `--ffmpeg-arg`/this key refine or append after — see
[Profiles](#profiles-section).

```yaml
transcode:
  ffmpeg_args: ["-vf", "scale=1280:-2"]
```

#### `transcode.allow_hdr_loss`

| Attribute | Value |
|---|---|
| Type | `bool` |
| Default | `false` |
| CLI flag | `--i-know-this-drops-hdr` |
| Env var | `SMELT_ALLOW_HDR_LOSS` |

Required to transcode a source carrying a Dolby Vision RPU (detected via
`ffprobe`'s DOVI configuration record). Without it, matching files are
excluded from the plan and counted separately from `skipped` — smelt has no
DV passthrough, so a plain re-encode would silently drop the RPU layer. Set
this only once you've made that call knowingly; it does not affect static
HDR10/HDR10+ metadata, which is a separate, currently undetected gap (see
[Planned / Under Consideration](../README.md#planned--under-consideration)
in the README).

```yaml
transcode:
  allow_hdr_loss: true
```

---

### `profiles` section

Named profiles group codec, CRF, preset, and extra ffmpeg arguments under a
single key. Apply a profile with `--profile <name>` or set it in config.

When `--profile` is specified, it registers the profile's `codec`/`crf`/`preset`
as **built-in defaults**, not overrides — so the actual precedence is
`explicit CLI flag > explicit transcode.codec/crf/preset (config.yaml) >
profile > built-in default`. A profile only fills in values you haven't set
elsewhere; it never overrides an explicit `transcode.*` key already present in
config.yaml. `extra_args` are appended to the ffmpeg argument list after all
computed flags (see [`transcode.ffmpeg_args`](#transcodeffmpeg_args), which
combines with it the same way).

```yaml
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

  mobile:
    codec: h264
    crf: 30
    preset: fast
    extra_args: ["-vf", "scale=1280:-2", "-movflags", "+faststart"]
```

#### Profile fields

| Field | Type | Description |
|---|---|---|
| `codec` | string | Same values as `transcode.codec` |
| `crf` | int | Same range as `transcode.crf` |
| `preset` | string | Same values as `transcode.preset` |
| `extra_args` | []string | Raw ffmpeg arguments appended after computed flags |

---

## Precedence

Settings are resolved in this order (highest wins):

```
CLI flag  >  environment variable  >  config.yaml  >  built-in default
```

---

## Minimal config.yaml

```yaml
smelt:
  workers: 8

transcode:
  src: /mnt/media
  codec: h265
  crf: 20
  preset: slow
  inplace: false
```

---

## Full Annotated Example

See [config.yaml.example](../config.yaml.example) in the project root, or
generate it locally with:

```bash
smelt config init
```
