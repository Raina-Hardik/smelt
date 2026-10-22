package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCodecName(t *testing.T) {
	cases := map[string]string{
		"h265": "hevc", "hevc": "hevc", "H265": "hevc",
		"h264": "h264", "avc": "h264",
		"av1": "av1", "vp9": "vp9",
		"bogus": "",
	}
	for in, want := range cases {
		if got := CodecName(in); got != want {
			t.Errorf("CodecName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEncoderName(t *testing.T) {
	cases := map[[2]string]string{
		{"h265", "nvenc"}:  "hevc_nvenc",
		{"hevc", "vaapi"}:  "hevc_vaapi",
		{"h264", "qsv"}:    "h264_qsv",
		{"av1", "amf"}:     "av1_amf",
		{"bogus", "nvenc"}: "",
	}
	for in, want := range cases {
		if got := encoderName(in[0], in[1]); got != want {
			t.Errorf("encoderName(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

// Software modes resolve deterministically without probing hardware.
func TestResolveEncoderSoftware(t *testing.T) {
	for _, mode := range []string{"none", "", "software", "sw"} {
		enc, backend := ResolveEncoder(context.Background(), "h265", mode)
		if enc != "libx265" || backend != "" {
			t.Errorf("ResolveEncoder(h265, %q) = (%q, %q), want (libx265, \"\")", mode, enc, backend)
		}
	}
}

func TestIsKnownHWAccel(t *testing.T) {
	for _, s := range []string{"auto", "none", "nvenc", "qsv", "vaapi", "amf", "videotoolbox"} {
		if !IsKnownHWAccel(s) {
			t.Errorf("IsKnownHWAccel(%q) = false, want true", s)
		}
	}
	if IsKnownHWAccel("turbo") {
		t.Error("IsKnownHWAccel(turbo) = true, want false")
	}
}

// With --hwaccel auto and no usable hardware encoder, the requested codec must
// be kept and encoded in software (regression: av1 used to become hevc).
func TestResolveEncoderAutoKeepsRequestedCodec(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	for codec, want := range map[string]string{
		"av1":  "libsvtav1",
		"h265": "libx265",
		"h264": "libx264",
	} {
		enc, backend := ResolveEncoder(context.Background(), codec, "auto")
		if enc != want || backend != "" {
			t.Errorf("ResolveEncoder(%q, auto) = (%q, %q), want (%q, \"\")", codec, enc, backend, want)
		}
	}
}
