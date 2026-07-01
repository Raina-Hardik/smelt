# Contributing to smelt

Thank you for your interest in smelt! This document covers everything you need
to get a working development environment, understand the project structure, and
contribute code or documentation.

---

## Prerequisites

| Tool | Minimum version | Purpose |
|---|---|---|
| Go | 1.26 | Build and test |
| ffmpeg + ffprobe | 4.4 | Runtime dependency for tests and manual testing |
| git | 2.30 | Version control |
| golangci-lint | 2.x | Linting (config is `.golangci.yml`, schema version 2) |

Install ffmpeg on common platforms:

```bash
# Ubuntu / Debian
sudo apt install ffmpeg

# macOS (Homebrew)
brew install ffmpeg

# Arch Linux
sudo pacman -S ffmpeg
```

---

## Getting the Source

```bash
git clone https://github.com/Raina-Hardik/smelt
cd smelt
go mod download
just install-hooks   # wire up the pre-commit hook (one-time)
```

---

## Build

```bash
# Development build
go build -o smelt .

# With version injection (note: `version` lives in package cmd, not main)
go build -ldflags="-X github.com/Raina-Hardik/smelt/cmd.version=v0.8.0-dev" -o smelt .
```

---

## Running Tests

```bash
# Run all tests
go test ./...

# Run tests with race detector (recommended before PRs)
go test -race ./...

# Run a specific package
go test ./internal/ffmpeg/...

# Verbose output
go test -v ./...
```

Integration tests that invoke real ffmpeg processes are tagged with
`//go:build integration` and are skipped by default. To run them:

```bash
go test -tags integration ./...
```

These require ffmpeg and ffprobe on `$PATH` and may take several seconds.

---

## Local Dev Hooks

The pre-commit hook in `.githooks/pre-commit` auto-formats staged Go files with
`gofmt`, runs `go vet`, and runs `golangci-lint`. Install it once after cloning:

```bash
just install-hooks
```

This sets `core.hooksPath = .githooks` in your local git config. The hook is
non-blocking if `golangci-lint` is not on `$PATH` (a warning is printed instead).

## Linting

```bash
golangci-lint run ./...
```

The project uses `.golangci.yml` (at the repo root) to configure enabled
linters. Lint must pass before a PR is merged.

---

## Dev Workflow

### Iterating on the CLI

```bash
# Build and run in one step
go run . transcode --src ./testdata --ext mkv --dry-run

# Run the TUI against test fixtures
go run . tui --src ./testdata
```

### Watching for changes

```bash
# Using entr (install separately)
find . -name '*.go' | entr -r go run . tui --src ./testdata
```

### Generating a test media file

```bash
# 30-second silent test video (requires ffmpeg)
ffmpeg -f lavfi -i "color=c=blue:s=640x360:d=30" \
       -f lavfi -i anullsrc=channel_layout=stereo:sample_rate=44100 \
       -c:v libx264 -crf 23 -t 30 \
       testdata/sample.mp4
```

---

## Project Conventions

### Code style

- **No comments by default.** Add a comment only when the *why* is non-obvious:
  a hidden constraint, a subtle invariant, or a workaround for a specific bug.
- **No placeholder TODOs in merged code.** Stubs must be valid Go.
- **Error wrapping**: always use `%w` in `fmt.Errorf` so errors are unwrappable.
- **Context first**: any function that may block or exec a process must accept
  `context.Context` as its first parameter.
- **Typed errors**: `internal/ffmpeg` exposes `ExecError` and `OSError`. New
  error types must be added to that package and exported for callers to switch on.

### Commit messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add VP9 profile preset
fix: handle ffprobe timeout for corrupt files
docs: add output_dir to CONFIG.md
refactor: extract codec mapping to internal table
test: add integration test for --inplace flag
```

### Branch names

```
feat/<short-description>
fix/<short-description>
docs/<short-description>
```

Design notes, brainstorms, and half-baked proposals do **not** belong on
`master`. Keep them on long-lived idea branches so they stay available without
polluting the mainline:

```
ideas/<topic>   # design docs, implementation sketches, "what if we…" writeups
exp/<topic>     # experimental / flaky spikes not ready for review
```

For example, the surgical DV/HDR design lives on `ideas/surgical-mode`
(`docs/ideas-implementation.md`) rather than being merged to `master`.

---

## Adding a New Transcode Profile

Profiles live in `config.yaml` under the `profiles` key. To add a new built-in
profile (available without user config), you also register it as a named
constant in the codebase.

### Step 1 — Define in config.yaml.example

```yaml
profiles:
  my_profile:
    codec: h264
    crf: 26
    preset: fast
    extra_args: ["-vf", "scale=1920:-2"]
```

### Step 2 — Register in `internal/ffmpeg/runner.go`

Add the profile name to any validation logic (when implemented) so that smelt
can give a useful error if an unknown profile is requested.

### Step 3 — Document in CONFIG.md

Add a row to the profiles table in [CONFIG.md](CONFIG.md) describing the
profile's intended use case and the codec/CRF/preset it configures.

### Step 4 — Add a test

Add a test in `internal/worker/pool_test.go` (or a dedicated
`profiles_test.go`) verifying that the profile is applied correctly to the
ffmpeg argument list.

---

## Adding a New Codec

1. **Add the alias** in `internal/ffmpeg/runner.go`'s `codecFlag()` function:
   ```go
   case "prores":
       return "prores_ks"
   ```

2. **Document it** in the `transcode.codec` table in [CONFIG.md](CONFIG.md)
   and the `--codec` flag description in [CLI.md](CLI.md).

3. **Test it** — add a case to the `TestCodecFlag` table-driven test in
   `internal/ffmpeg/runner_test.go`.

---

## Releases

Releases are built automatically by CI when a tag matching `v*` is pushed.
The release job runs only after `test` and `lint` both pass.

**To cut a release:**

```bash
just tag v1.2.3   # runs the full CI gate locally, then pushes the tag
```

The `just tag` recipe refuses to push if any local check fails.

CI then cross-compiles five binaries and publishes them to the GitHub release:

| Asset | OS | Arch |
|---|---|---|
| `smelt-linux-amd64` | Linux | x86-64 |
| `smelt-linux-arm64` | Linux | ARM64 |
| `smelt-windows-amd64.exe` | Windows | x86-64 |
| `smelt-darwin-amd64` | macOS | Intel |
| `smelt-darwin-arm64` | macOS | Apple Silicon |

All Linux builds are fully static (`CGO_ENABLED=0`), so they run on both
glibc-based (Ubuntu, Fedora) and musl-based (Alpine) distributions without
any runtime dependency.

---

## Pull Request Checklist

- [ ] `go build ./...` passes
- [ ] `go test -race ./...` passes
- [ ] `golangci-lint run ./...` passes
- [ ] New flags are documented in [CLI.md](CLI.md)
- [ ] New config keys are documented in [CONFIG.md](CONFIG.md)
- [ ] Commit messages follow Conventional Commits
- [ ] No new external dependencies without discussion in the PR description

---

## Reporting Issues

Open an issue at https://github.com/Raina-Hardik/smelt/issues. Include:

- smelt version (`smelt version`)
- ffmpeg version (`ffmpeg -version | head -1`)
- OS and architecture
- The exact command you ran
- Full log output (run with `--log-level debug`)
