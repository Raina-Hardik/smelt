//go:build integration

package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// genSample writes a tiny real media file; extra args select codec/pix_fmt.
func genSample(t *testing.T, name string, encArgs ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	args := []string{"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=s=320x240:d=1",
		"-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=44100",
		"-shortest"}
	args = append(args, encArgs...)
	args = append(args, "-c:a", "aac", path)
	if err := exec.Command("ffmpeg", args...).Run(); err != nil {
		t.Skipf("cannot generate test media: %v", err)
	}
	return path
}

// requireBackend skips unless the backend's encoder probe passes on this
// machine — each test degrades to a clean skip without the hardware/driver.
func requireBackend(t *testing.T, enc, backend string) {
	t.Helper()
	if !probeEncoder(context.Background(), enc, backend) {
		t.Skipf("no usable %s device here", backend)
	}
}

// TestVAAPIFullPipelineIntegration runs the complete hardware decode → encode
// pipeline against real 8-bit and 10-bit samples on the VAAPI device.
func TestVAAPIFullPipelineIntegration(t *testing.T) {
	requireBackend(t, "hevc_vaapi", "vaapi")

	cases := []struct {
		name  string
		gen   []string
		depth int
	}{
		{"8bit-h264", []string{"-c:v", "libx264", "-pix_fmt", "yuv420p"}, 8},
		{"10bit-hevc", []string{"-c:v", "libx265", "-pix_fmt", "yuv420p10le", "-x265-params", "log-level=none"}, 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := genSample(t, "in.mp4", c.gen...)
			attrs, err := ProbeAttrs(context.Background(), src)
			if err != nil {
				t.Fatalf("ProbeAttrs: %v", err)
			}
			if got := HWPipelineBitDepth(attrs.PixFmt); got != c.depth {
				t.Fatalf("HWPipelineBitDepth(%s) = %d, want %d", attrs.PixFmt, got, c.depth)
			}
			if !ProbeDecode(context.Background(), "vaapi", src, attrs) {
				t.Skipf("device cannot hw-decode %s/%s", attrs.VideoCodec, attrs.PixFmt)
			}
			if c.depth == 10 && !ProbeEncoder10Bit(context.Background(), "hevc_vaapi", "vaapi") {
				t.Skip("device cannot encode main10")
			}
			dst := filepath.Join(t.TempDir(), "out.mkv")
			spec := EncodeSpec{
				Codec: "h265", CRF: 28, Encoder: "hevc_vaapi", Backend: "vaapi",
				DecodeBackend: "vaapi", SourceBitDepth: c.depth, AudioCodec: "copy", SubtitleMode: "drop",
			}
			if err := Run(context.Background(), src, dst, spec, nil); err != nil {
				t.Fatalf("full pipeline Run: %v", err)
			}
			out, err := ProbeAttrs(context.Background(), dst)
			if err != nil {
				t.Fatalf("probe output: %v", err)
			}
			if out.VideoCodec != "hevc" {
				t.Errorf("output codec = %q, want hevc", out.VideoCodec)
			}
			if out.BitDepth != c.depth {
				t.Errorf("output bit depth = %d, want %d (no silent flattening)", out.BitDepth, c.depth)
			}
			if _, err := os.Stat(dst); err != nil {
				t.Errorf("output missing: %v", err)
			}
		})
	}
}

// TestQSVFullPipelineIntegration mirrors the VAAPI test on the QSV device.
func TestQSVFullPipelineIntegration(t *testing.T) {
	requireBackend(t, "hevc_qsv", "qsv")

	src := genSample(t, "in.mp4", "-c:v", "libx265", "-pix_fmt", "yuv420p10le", "-x265-params", "log-level=none")
	attrs, err := ProbeAttrs(context.Background(), src)
	if err != nil {
		t.Fatalf("ProbeAttrs: %v", err)
	}
	if !ProbeDecode(context.Background(), "qsv", src, attrs) {
		t.Skip("device cannot hw-decode hevc 10-bit via qsv")
	}
	if !ProbeEncoder10Bit(context.Background(), "hevc_qsv", "qsv") {
		t.Skip("device cannot encode main10 via qsv")
	}
	dst := filepath.Join(t.TempDir(), "out.mkv")
	spec := EncodeSpec{
		Codec: "h265", CRF: 28, Encoder: "hevc_qsv", Backend: "qsv",
		DecodeBackend: "qsv", SourceBitDepth: 10, AudioCodec: "copy", SubtitleMode: "drop",
	}
	if err := Run(context.Background(), src, dst, spec, nil); err != nil {
		t.Fatalf("qsv full pipeline Run: %v", err)
	}
	out, err := ProbeAttrs(context.Background(), dst)
	if err != nil {
		t.Fatalf("probe output: %v", err)
	}
	if out.VideoCodec != "hevc" || out.BitDepth != 10 {
		t.Errorf("output = %s/%d-bit, want hevc/10-bit", out.VideoCodec, out.BitDepth)
	}
}

// TestCUDAFullPipelineIntegration mirrors the same on NVDEC → NVENC.
func TestCUDAFullPipelineIntegration(t *testing.T) {
	requireBackend(t, "hevc_nvenc", "nvenc")

	src := genSample(t, "in.mp4", "-c:v", "libx265", "-pix_fmt", "yuv420p10le", "-x265-params", "log-level=none")
	attrs, err := ProbeAttrs(context.Background(), src)
	if err != nil {
		t.Fatalf("ProbeAttrs: %v", err)
	}
	if !ProbeDecode(context.Background(), "nvenc", src, attrs) {
		t.Skip("device cannot hw-decode hevc 10-bit via nvdec")
	}
	if !ProbeEncoder10Bit(context.Background(), "hevc_nvenc", "nvenc") {
		t.Skip("device cannot encode main10 via nvenc")
	}
	dst := filepath.Join(t.TempDir(), "out.mkv")
	spec := EncodeSpec{
		Codec: "h265", CRF: 28, Encoder: "hevc_nvenc", Backend: "nvenc",
		DecodeBackend: "nvenc", SourceBitDepth: 10, AudioCodec: "copy", SubtitleMode: "drop",
	}
	if err := Run(context.Background(), src, dst, spec, nil); err != nil {
		t.Fatalf("cuda full pipeline Run: %v", err)
	}
	out, err := ProbeAttrs(context.Background(), dst)
	if err != nil {
		t.Fatalf("probe output: %v", err)
	}
	if out.VideoCodec != "hevc" || out.BitDepth != 10 {
		t.Errorf("output = %s/%d-bit, want hevc/10-bit", out.VideoCodec, out.BitDepth)
	}
}

// TestProbeDecodeNegativeIntegration: a software-only shape must probe false
// without breaking anything (probe design covers missing hwaccels the same
// way as missing devices).
func TestProbeDecodeNegativeIntegration(t *testing.T) {
	src := genSample(t, "in.mp4", "-c:v", "libx264", "-pix_fmt", "yuv444p")
	attrs, err := ProbeAttrs(context.Background(), src)
	if err != nil {
		t.Fatalf("ProbeAttrs: %v", err)
	}
	// amf/videotoolbox are hard-coded encode-only: always false, no probe run.
	for _, b := range []string{"amf", "videotoolbox"} {
		if ProbeDecode(context.Background(), b, src, attrs) {
			t.Errorf("ProbeDecode(%s) = true, want false (encode-only backend)", b)
		}
	}
}
