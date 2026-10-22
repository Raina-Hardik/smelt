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

// TestProbeDolbyVisionNegative checks that a plain (non-DV) h264 sample is not
// flagged. Positive detection (an actual DOVI configuration record) isn't
// covered here — fabricating a valid DV bitstream is out of scope for a unit
// test; ProbeDolbyVision's string match was verified manually against a real
// DV remux (see the field report this flag was added for).
func TestProbeDolbyVisionNegative(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.mp4")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=s=64x64:d=1", "-c:v", "libx264", "-t", "1", path)
	if err := gen.Run(); err != nil {
		t.Skipf("cannot generate test media (ffmpeg unavailable?): %v", err)
	}

	isDV, err := ProbeDolbyVision(context.Background(), path)
	if err != nil {
		t.Fatalf("ProbeDolbyVision: %v", err)
	}
	if isDV {
		t.Error("ProbeDolbyVision = true for a plain h264 sample, want false")
	}
}

// TestHDR10RoundTripIntegration encodes an HDR10 source through Run for each
// software encoder and verifies colour tags and static metadata survive.
func TestHDR10RoundTripIntegration(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=s=320x240:d=1:r=24", "-f", "lavfi", "-i", "anullsrc=d=1",
		"-pix_fmt", "yuv420p10le", "-c:v", "libx265", "-c:a", "aac", "-x265-params",
		"hdr10=1:repeat-headers=1:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:"+
			"master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50):max-cll=1000,400:log-level=error",
		src)
	if err := gen.Run(); err != nil {
		t.Skipf("cannot generate HDR10 media (libx265 unavailable?): %v", err)
	}
	ctx := context.Background()
	in, err := ProbeHDR(ctx, src)
	if err != nil || in == nil || !in.IsPQ() {
		t.Fatalf("source ProbeHDR = %+v, %v", in, err)
	}

	for _, codec := range []string{"h265", "av1"} {
		dst := filepath.Join(dir, codec+".mkv")
		spec := EncodeSpec{Codec: codec, CRF: 30, Preset: "ultrafast", HDR: in}
		if err := Run(ctx, src, dst, spec, nil); err != nil {
			t.Fatalf("%s encode: %v", codec, err)
		}
		out, err := ProbeHDR(ctx, dst)
		if err != nil || out == nil {
			t.Fatalf("%s: ProbeHDR = %+v, %v", codec, out, err)
		}
		if !out.IsPQ() || out.ColorPrimaries != "bt2020" || out.ColorSpace != "bt2020nc" {
			t.Errorf("%s: colour tags lost: %+v", codec, out)
		}
		if out.MaxCLL != 1000 || out.MaxFALL != 400 {
			t.Errorf("%s: content light level = %d/%d", codec, out.MaxCLL, out.MaxFALL)
		}
		if out.Mastering == nil || out.Mastering.x265MasterDisplay() != in.Mastering.x265MasterDisplay() {
			t.Errorf("%s: mastering display = %+v, want %+v", codec, out.Mastering, in.Mastering)
		}
	}
}
