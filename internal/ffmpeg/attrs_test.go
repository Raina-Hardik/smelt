package ffmpeg

import "testing"

func TestBitDepthFromPixFmt(t *testing.T) {
	cases := map[string]int{
		"":            0,
		"yuv420p":     8,
		"yuvj420p":    8,
		"nv12":        8,
		"yuv420p10le": 10,
		"p010le":      10,
		"yuv422p10le": 10,
		"yuv420p12le": 12,
		"p016le":      16,
		"gray16be":    16,
		"yuv444p":     8,
	}
	for pf, want := range cases {
		if got := bitDepthFromPixFmt(pf); got != want {
			t.Errorf("bitDepthFromPixFmt(%q) = %d, want %d", pf, got, want)
		}
	}
}

func TestHWPipelineBitDepth(t *testing.T) {
	cases := map[string]int{
		"yuv420p":     8,
		"yuvj420p":    8,
		"nv12":        8,
		"nv21":        8,
		"yuv420p10le": 10,
		"p010le":      10,
		"yuv422p10le": 0, // 4:2:2 → software pipeline
		"yuv444p":     0, // 4:4:4 → software pipeline
		"yuv420p12le": 0, // 12-bit → software pipeline
		"":            0,
	}
	for pf, want := range cases {
		if got := HWPipelineBitDepth(pf); got != want {
			t.Errorf("HWPipelineBitDepth(%q) = %d, want %d", pf, got, want)
		}
	}
}

func TestParseAttrs(t *testing.T) {
	// Canned ffprobe -of default=noprint_wrappers=1 output in ffprobe's real
	// field order: codec_name and profile print *before* codec_type within
	// each stream (verified against ffmpeg n8.1.2). The audio stream's
	// codec_name/profile must not clobber the video stream's.
	out := `codec_name=hevc
profile=Main 10
codec_type=video
width=3840
height=2160
pix_fmt=yuv420p10le
codec_name=aac
profile=LC
codec_type=audio
duration=4242.5
bit_rate=15000000
`
	a := parseAttrs(out)
	if a.VideoCodec != "hevc" || a.AudioCodec != "aac" {
		t.Errorf("codecs = %q/%q", a.VideoCodec, a.AudioCodec)
	}
	if a.Profile != "Main 10" {
		t.Errorf("Profile = %q, want Main 10", a.Profile)
	}
	if a.PixFmt != "yuv420p10le" || a.BitDepth != 10 {
		t.Errorf("PixFmt/BitDepth = %q/%d, want yuv420p10le/10", a.PixFmt, a.BitDepth)
	}
	if a.Width != 3840 || a.Height != 2160 {
		t.Errorf("dims = %dx%d", a.Width, a.Height)
	}
	if a.BitRate != 15000 || a.DurationS != 4242.5 {
		t.Errorf("bitrate/duration = %d/%f", a.BitRate, a.DurationS)
	}
}
