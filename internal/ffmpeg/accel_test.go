package ffmpeg

import (
	"context"
	"testing"
)

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
