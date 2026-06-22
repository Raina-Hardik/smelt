# smelt — task runner. Run `just` or `just --list` to see recipes.
# Mirrors the commands documented in CLAUDE.md.

# Inject version into the `cmd` package (NOT main — `version` lives in cmd/version.go).
version := "v0.2.0-dev"
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

# Build, vet, and race-test — the pre-PR gate.
check: build vet test-race

# Run transcode against testdata without building.
run-transcode *ARGS:
    go run . transcode --src ./testdata {{ARGS}}

# Launch the TUI against testdata without building.
run-tui *ARGS:
    go run . tui --src ./testdata {{ARGS}}

# Generate a 30s test media file at testdata/sample.mp4 (requires ffmpeg).
testdata:
    mkdir -p testdata
    ffmpeg -f lavfi -i "color=c=blue:s=640x360:d=30" \
           -f lavfi -i anullsrc=channel_layout=stereo:sample_rate=44100 \
           -c:v libx264 -crf 23 -t 30 testdata/sample.mp4

# Remove build artifacts.
clean:
    rm -f smelt
