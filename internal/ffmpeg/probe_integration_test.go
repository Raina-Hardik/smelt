//go:build integration

package ffmpeg

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestProbeVideoCodecIntegration generates a tiny real h264 file and probes it.
// Requires ffmpeg and ffprobe on $PATH (run with: go test -tags integration ./...).
func TestProbeVideoCodecIntegration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.mp4")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=s=64x64:d=1", "-c:v", "libx264", "-t", "1", path)
	if err := gen.Run(); err != nil {
		t.Skipf("cannot generate test media (ffmpeg unavailable?): %v", err)
	}

	got, err := ProbeVideoCodec(context.Background(), path)
	if err != nil {
		t.Fatalf("ProbeVideoCodec: %v", err)
	}
	if got != "h264" {
		t.Errorf("ProbeVideoCodec = %q, want h264", got)
	}
}
