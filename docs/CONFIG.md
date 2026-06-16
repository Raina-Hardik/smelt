# smelt Configuration Reference

smelt uses a single YAML config file. Run `smelt config init` to generate a
fully annotated starter.

---

## File Discovery

smelt searches for a config file in this order:

1. Path given by `--config` (if provided)
2. `./config.yaml` (current working directory)
3. `$HOME/.config/smelt/config.yaml`

If no file is found, built-in defaults apply. No error is raised.

---

## Schema

```yaml
smelt:
  workers: 4                  # int    — parallel ffmpeg jobs; 0 = runtime.NumCPU()
  log_level: info             # string — debug | info | warn | error
  log_format: auto            # string — auto | json | pretty

transcode:
  src: ""                     # string — source directory path (required)
  ext: [mkv, mp4, avi]        # []string — extensions to match, no leading dot
  codec: h265                 # string — h264 | h265 | av1 | vp9
  crf: 23                     # int    — constant rate factor, 0–51
  preset: medium              # string — ffmpeg preset identifier
  inplace: false              # bool   — replace original after successful transcode
  output_dir: ""              # string — redirect output here; empty = alongside source
  suffix: ".smelt"            # string — temp filename suffix during active transcode

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

`auto` selects `pretty` (colorized, human-readable) when stdout is a TTY, and
`json` (newline-delimited JSON objects) otherwise. Use `json` explicitly when
piping to log aggregators.

```yaml
smelt:
  log_format: json
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
stripped automatically, so `mkv` and `.mkv` are equivalent.

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

ffmpeg encoding preset. Faster presets encode quicker at the cost of larger
output files at the same CRF. Valid H.264/H.265 presets from fastest to
slowest: `ultrafast`, `superfast`, `veryfast`, `faster`, `fast`, `medium`,
`slow`, `slower`, `veryslow`.

```yaml
transcode:
  preset: slow
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

```yaml
transcode:
  inplace: false
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
| CLI flag | _(config only)_ |
| Env var | `SMELT_SUFFIX` |

Temporary filename suffix applied during an active transcode. The file is
renamed to remove this suffix on success, or deleted on failure. Prevents
partial output files from being mistaken for complete ones.

```yaml
transcode:
  suffix: .tmp
```

---

### `profiles` section

Named profiles group codec, CRF, preset, and extra ffmpeg arguments under a
single key. Apply a profile with `--profile <name>` or set it in config.

When `--profile` is specified, it overrides `transcode.codec`, `transcode.crf`,
and `transcode.preset`. `extra_args` are appended to the ffmpeg argument list
after all computed flags.

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
