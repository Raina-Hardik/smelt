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
		{"nvenc passes native preset", EncodeSpec{Encoder: "hevc_nvenc", Backend: "nvenc", CRF: 23, Preset: "p5"},
			[]string{"-c:v", "hevc_nvenc", "-rc", "vbr", "-cq", "23", "-preset", "p5"}},
		{"nvenc maps x264 preset to its namespace", EncodeSpec{Encoder: "hevc_nvenc", Backend: "nvenc", CRF: 23, Preset: "superfast"},
			[]string{"-c:v", "hevc_nvenc", "-rc", "vbr", "-cq", "23", "-preset", "fast"}},
		{"qsv uses -q:v (CQP)", EncodeSpec{Encoder: "h264_qsv", Backend: "qsv", CRF: 25},
			[]string{"-c:v", "h264_qsv", "-q:v", "25"}},
		{"qsv maps superfast to veryfast", EncodeSpec{Encoder: "h264_qsv", Backend: "qsv", CRF: 25, Preset: "superfast"},
			[]string{"-c:v", "h264_qsv", "-q:v", "25", "-preset", "veryfast"}},
		{"qsv keeps a valid preset", EncodeSpec{Encoder: "h264_qsv", Backend: "qsv", CRF: 25, Preset: "slow"},
			[]string{"-c:v", "h264_qsv", "-q:v", "25", "-preset", "slow"}},
		{"av1 maps name to a number", EncodeSpec{Codec: "av1", CRF: 30, Preset: "medium"},
			[]string{"-c:v", "libsvtav1", "-crf", "30", "-preset", "6"}},
		{"vaapi uses -qp, no preset", EncodeSpec{Encoder: "hevc_vaapi", Backend: "vaapi", CRF: 24, Preset: "medium"},
			[]string{"-c:v", "hevc_vaapi", "-rc_mode", "CQP", "-qp", "24"}},
	}
	for _, c := range cases {
		if got := rateControlArgs(c.spec); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: rateControlArgs = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestAudioArgs(t *testing.T) {
	cases := []struct {
		name string
		spec EncodeSpec
		want []string
	}{
		{"default copies audio", EncodeSpec{}, []string{"-c:a", "copy"}},
		{"explicit copy", EncodeSpec{AudioCodec: "copy"}, []string{"-c:a", "copy"}},
		{"aac without bitrate", EncodeSpec{AudioCodec: "aac"}, []string{"-c:a", "aac"}},
		{"aac with bitrate", EncodeSpec{AudioCodec: "aac", AudioBitrate: "192k"}, []string{"-c:a", "aac", "-b:a", "192k"}},
		{"opus aliases to libopus", EncodeSpec{AudioCodec: "opus", AudioBitrate: "128k"}, []string{"-c:a", "libopus", "-b:a", "128k"}},
		{"raw encoder passthrough", EncodeSpec{AudioCodec: "libfdk_aac"}, []string{"-c:a", "libfdk_aac"}},
	}
	for _, c := range cases {
		if got := audioArgs(c.spec); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: audioArgs = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPresetsFor(t *testing.T) {
	cases := []struct {
		backend, encoder string
		want             []string
	}{
		{"nvenc", "hevc_nvenc", nvencPresets},
		{"qsv", "h264_qsv", qsvPresets},
		{"", "libx265", x264Presets},
		{"", "libsvtav1", svtPresets},
		{"", "libvpx-vp9", nil},
		{"vaapi", "hevc_vaapi", nil},
		{"amf", "hevc_amf", nil},
	}
	for _, c := range cases {
		if got := PresetsFor(c.backend, c.encoder); !reflect.DeepEqual(got, c.want) {
			t.Errorf("PresetsFor(%q,%q) = %v, want %v", c.backend, c.encoder, got, c.want)
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
		"-map", "0:v:0", "-map", "0:a", "-map", "0:s?", "-c:s", "copy",
		"-c:v", "libx265", "-crf", "23", "-preset", "medium",
		"-c:a", "copy",
		"-movflags", "+faststart",
		"-y", "out.mkv",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildArgs =\n  %v\nwant\n  %v", got, want)
	}
}

func TestBuildArgsSubsDrop(t *testing.T) {
	spec := EncodeSpec{Codec: "h265", CRF: 23, SubtitleMode: "drop"}
	got := buildArgs("in.mkv", "out.mkv", spec)
	found := false
	for _, a := range got {
		if a == "-sn" {
			found = true
		}
	}
	if !found {
		t.Errorf("--subs=drop: expected -sn in args, got %v", got)
	}
}

// VAAPI requires the device selected before -i and a hwupload filter after it.
func TestVAAPIPipelineOrder(t *testing.T) {
	got := buildArgs("in.mkv", "out.mkv", EncodeSpec{Encoder: "hevc_vaapi", Backend: "vaapi", CRF: 24})
	dev, in, vf := -1, -1, -1
	for i, a := range got {
		switch a {
		case "-vaapi_device":
			dev = i
		case "-i":
			in = i
		case "-vf":
			vf = i
		}
	}
	if dev < 0 || in < 0 || vf < 0 || dev >= in || in >= vf {
		t.Errorf("vaapi arg order wrong (device=%d input=%d filter=%d): %v", dev, in, vf, got)
	}
}

func TestContainerArgs(t *testing.T) {
	// mp4 + hevc → faststart + hvc1 tag
	got := containerArgs(EncodeSpec{Codec: "h265", Container: "mp4"})
	want := []string{"-movflags", "+faststart", "-tag:v", "hvc1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mp4+hevc container args = %v, want %v", got, want)
	}
	// mp4 + h264 → faststart only, no hvc1
	got = containerArgs(EncodeSpec{Codec: "h264", Container: "mp4"})
	want = []string{"-movflags", "+faststart"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mp4+h264 container args = %v, want %v", got, want)
	}
	// mkv → no special muxer flags
	if got := containerArgs(EncodeSpec{Codec: "h265", Container: "mkv"}); got != nil {
		t.Errorf("mkv container args = %v, want nil", got)
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
