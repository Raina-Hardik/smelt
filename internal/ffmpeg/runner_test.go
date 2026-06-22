package ffmpeg

import (
	"reflect"
	"testing"
	"time"
)

func TestCodecFlag(t *testing.T) {
	cases := map[string]string{
		"h265":    "libx265",
		"hevc":    "libx265",
		"H265":    "libx265", // case-insensitive
		"h264":    "libx264",
		"avc":     "libx264",
		"av1":     "libsvtav1",
		"vp9":     "libvpx-vp9",
		"libx264": "libx264", // unknown alias passes through verbatim
		"copy":    "copy",
	}
	for in, want := range cases {
		if got := codecFlag(in); got != want {
			t.Errorf("codecFlag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRateControlArgs(t *testing.T) {
	cases := []struct {
		name string
		spec EncodeSpec
		want []string
	}{
		{"h265 crf+preset", EncodeSpec{Codec: "h265", CRF: 23, Preset: "medium"},
			[]string{"-c:v", "libx265", "-crf", "23", "-preset", "medium"}},
		{"h264 crf+preset", EncodeSpec{Codec: "h264", CRF: 22, Preset: "fast"},
			[]string{"-c:v", "libx264", "-crf", "22", "-preset", "fast"}},
		{"av1 takes preset", EncodeSpec{Codec: "av1", CRF: 30, Preset: "8"},
			[]string{"-c:v", "libsvtav1", "-crf", "30", "-preset", "8"}},
		{"vp9 needs -b:v 0, drops preset", EncodeSpec{Codec: "vp9", CRF: 31, Preset: "medium"},
			[]string{"-c:v", "libvpx-vp9", "-crf", "31", "-b:v", "0"}},
		{"empty preset omits flag", EncodeSpec{Codec: "h264", CRF: 20},
			[]string{"-c:v", "libx264", "-crf", "20"}},
	}
	for _, c := range cases {
		if got := rateControlArgs(c.spec); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: rateControlArgs = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBuildArgs(t *testing.T) {
	spec := EncodeSpec{
		Codec:     "h265",
		CRF:       23,
		Preset:    "medium",
		ExtraArgs: []string{"-movflags", "+faststart"},
	}
	got := buildArgs("in.mkv", "out.mkv", spec)
	want := []string{
		"-hide_banner", "-i", "in.mkv",
		"-c:v", "libx265", "-crf", "23", "-preset", "medium",
		"-c:a", "copy",
		"-movflags", "+faststart",
		"-y", "out.mkv",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildArgs =\n  %v\nwant\n  %v", got, want)
	}
}

func TestParseTime(t *testing.T) {
	d, ok := parseTime("frame=  120 fps= 24 time=00:01:23.45 bitrate=1000kbits/s")
	if !ok {
		t.Fatal("expected a match")
	}
	want := time.Minute + 23*time.Second + 450*time.Millisecond
	if d != want {
		t.Errorf("parseTime = %v, want %v", d, want)
	}
	if _, ok := parseTime("no timestamp on this line"); ok {
		t.Error("expected no match on a line without time=")
	}
}
