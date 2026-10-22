//go:build integration

package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestRunAudiolessSourceIntegration: a source with no audio stream must
// transcode (the audio map is optional) rather than fail on `-map 0:a`.
func TestRunAudiolessSourceIntegration(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "silent.mkv")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=s=64x64:d=1", "-c:v", "libx264", src)
	if err := gen.Run(); err != nil {
		t.Skipf("cannot generate test media: %v", err)
	}
	dst := filepath.Join(dir, "out.mkv")
	if err := Run(context.Background(), src, dst, EncodeSpec{Codec: "h264", CRF: 30, Preset: "ultrafast"}, nil); err != nil {
		t.Fatalf("Run on audio-less source: %v", err)
	}
}

// TestRunChunkedIntegration encodes a 12 s source in ~4 s chunks and checks
// the joined output: right duration and frame count, audio kept, work dir gone.
func TestRunChunkedIntegration(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=s=160x120:d=12:r=24", "-f", "lavfi", "-i", "sine=d=12",
		"-c:v", "libx264", "-g", "24", "-keyint_min", "24", "-sc_threshold", "0", "-c:a", "aac", src)
	if err := gen.Run(); err != nil {
		t.Skipf("cannot generate test media: %v", err)
	}
	dst := filepath.Join(dir, "src.transcoded.mkv")
	spec := EncodeSpec{
		Codec: "h265", CRF: 30, Preset: "ultrafast", SubtitleMode: "copy",
		ChunkDuration: 4 * time.Second, ChunkCooldown: 50 * time.Millisecond,
	}
	var last ProgressEvent
	if err := Run(context.Background(), src, dst, spec, func(ev ProgressEvent) { last = ev }); err != nil {
		t.Fatalf("chunked Run: %v", err)
	}
	if last.Percent != 1 {
		t.Errorf("final progress = %v, want 1", last.Percent)
	}
	if _, err := os.Stat(chunkDir(dst)); !os.IsNotExist(err) {
		t.Errorf("work dir %s should be removed (stat err = %v)", chunkDir(dst), err)
	}

	out, err := exec.Command("ffprobe", "-v", "error", "-count_frames",
		"-show_entries", "stream=codec_type,codec_name,nb_read_frames", "-of", "default=noprint_wrappers=1", dst).Output()
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"codec_name=hevc", "nb_read_frames=288", "codec_type=audio"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
	d, err := probeDuration(context.Background(), dst)
	if err != nil || d < 11*time.Second || d > 13*time.Second {
		t.Errorf("duration = %v, %v; want ~12s", d, err)
	}
}

// A cancelled chunked run must leave no work directory behind.
func TestRunChunkedCancelCleansUpIntegration(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=s=160x120:d=12:r=24", "-c:v", "libx264", "-g", "24", "-keyint_min", "24", "-sc_threshold", "0", src)
	if err := gen.Run(); err != nil {
		t.Skipf("cannot generate test media: %v", err)
	}
	dst := filepath.Join(dir, "src.transcoded.mkv")
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	spec := EncodeSpec{Codec: "h265", CRF: 30, Preset: "ultrafast", ChunkDuration: 4 * time.Second, ChunkCooldown: 30 * time.Second}
	if err := Run(ctx, src, dst, spec, nil); err == nil {
		t.Fatal("expected error from cancelled run")
	}
	if _, err := os.Stat(chunkDir(dst)); !os.IsNotExist(err) {
		t.Errorf("work dir should be removed after cancel (stat err = %v)", err)
	}
}

// HDR10 signalling must survive the split / per-segment encode / join path.
func TestRunChunkedPreservesHDR10Integration(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "hdr.mkv")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=s=160x120:d=8:r=24", "-pix_fmt", "yuv420p10le",
		"-c:v", "libx265", "-x265-params",
		"hdr10=1:repeat-headers=1:keyint=24:min-keyint=24:scenecut=0:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:"+
			"master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50):max-cll=1000,400:log-level=error",
		src)
	if err := gen.Run(); err != nil {
		t.Skipf("cannot generate HDR10 media: %v", err)
	}
	ctx := context.Background()
	in, err := ProbeHDR(ctx, src)
	if err != nil || in == nil {
		t.Fatalf("ProbeHDR(src) = %+v, %v", in, err)
	}
	dst := filepath.Join(dir, "hdr.transcoded.mkv")
	spec := EncodeSpec{Codec: "h265", CRF: 30, Preset: "ultrafast", HDR: in, ChunkDuration: 3 * time.Second}
	if err := Run(ctx, src, dst, spec, nil); err != nil {
		t.Fatalf("chunked Run: %v", err)
	}
	out, err := ProbeHDR(ctx, dst)
	if err != nil || out == nil || !out.IsPQ() {
		t.Fatalf("ProbeHDR(dst) = %+v, %v", out, err)
	}
	if out.MaxCLL != 1000 || out.MaxFALL != 400 || out.Mastering == nil ||
		out.Mastering.x265MasterDisplay() != in.Mastering.x265MasterDisplay() {
		t.Errorf("static metadata lost: %+v / %+v", out, out.Mastering)
	}
}
