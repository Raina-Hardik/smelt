# smelt — task runner. Run `just` or `just --list` to see recipes.
# Mirrors the commands documented in CLAUDE.md.

# Inject version into the `cmd` package (NOT main — `version` lives in cmd/version.go).
# Derived from git: exact tag at HEAD, else <tag>-<n>-g<sha>[-dirty], else "dev".
version := `git describe --tags --always --dirty 2>/dev/null || echo dev`
ldflags := "-X github.com/Raina-Hardik/smelt/cmd.version=" + version

_default:
    @just --list

# Build the smelt binary with version injection.
build:
    go build -ldflags="{{ldflags}}" -o smelt .

# Run all tests.
test:
    go test ./...

# Run all tests with the race detector (required before PRs).
test-race:
    go test -race ./...

# Run tests including those that invoke real ffmpeg/ffprobe.
test-integration:
    go test -tags integration ./...

# Verbose test output.
test-verbose:
    go test -v ./...

# Static analysis.
vet:
    go vet ./...

# Lint (requires golangci-lint on PATH).
lint:
    golangci-lint run ./...

# Regenerate api/gen.go from api/openapi.yaml.
generate:
    go generate ./...

# Fail if generated server code is stale relative to api/openapi.yaml.
generate-check: generate
    git diff --exit-code -- api/

# Refresh the vendored Scalar docs-UI bundle (internal/server/scalar.standalone.min.js).
# Bump the pinned version in the URL below and in docs.go's comment together.
vendor-scalar:
    curl -fsSL https://cdn.jsdelivr.net/npm/@scalar/api-reference@1.62.5/dist/browser/standalone.min.js \
        -o internal/server/scalar.standalone.min.js

# Build, vet, and race-test — the pre-PR gate.
check: build vet test-race

# Full CI gate: everything in `check` plus lint and the generated-code drift check.
ci: check lint generate-check

# Tag and push a release. Refuses unless the full CI gate (incl. lint) passes.
# Usage: just tag v0.1.0
tag VERSION: ci
    git tag -a {{VERSION}} -m "Release {{VERSION}}"
    git push origin {{VERSION}}

# Run transcode against testdata without building (clears prior outputs first).
run-transcode *ARGS: clean-testdata
    go run . transcode --src ./testdata {{ARGS}}

# Launch the TUI against testdata without building (clears prior outputs first).
run-tui *ARGS: clean-testdata
    go run . tui --src ./testdata {{ARGS}}

# Remove smelt-generated outputs from testdata so re-runs aren't skipped as idempotent.
clean-testdata:
    rm -f testdata/*.smelt.* testdata/*.transcoded.*

# Generate a 30s test media file at testdata/sample.mp4 (requires ffmpeg).
testdata:
    mkdir -p testdata
    ffmpeg -f lavfi -i "color=c=blue:s=640x360:d=30" \
           -f lavfi -i anullsrc=channel_layout=stereo:sample_rate=44100 \
           -c:v libx264 -crf 23 -t 30 testdata/sample.mp4

# Spawn a clean real-media testing env in ./smelt-sample-testcase/ (installs gum if needed).
sample-env:
    bash scripts/build-smelt-dataset.sh

# Install git hooks from .githooks/ (run once after cloning).
install-hooks:
    git config core.hooksPath .githooks
    @echo "Git hooks installed from .githooks/"

# Remove build artifacts.
clean:
    rm -f smelt
