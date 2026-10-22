package ffmpeg

import (
	"slices"
	"strings"
	"testing"
)

// Real ffprobe output (ffmpeg 8.1.3) for an HDR10 HEVC frame.
const hdr10FrameJSON = `{"frames":[{"color_range":"tv","color_space":"bt2020nc","color_primaries":"bt2020","color_transfer":"smpte2084","side_data_list":[{"side_data_type":"H.26[45] User Data Unregistered SEI message"},{"side_data_type":"Mastering display metadata","red_x":"34000/50000","red_y":"16000/50000","green_x":"13250/50000","green_y":"34500/50000","blue_x":"7500/50000","blue_y":"3000/50000","white_point_x":"15635/50000","white_point_y":"16450/50000","min_luminance":"50/10000","max_luminance":"10000000/10000"},{"side_data_type":"Content light level metadata","max_content":1000,"max_average":400}]}]}`

func TestParseHDR10(t *testing.T) {
	h, err := parseHDR([]byte(hdr10FrameJSON))
	if err != nil || h == nil {
		t.Fatalf("parseHDR = %v, %v", h, err)
	}
	if !h.IsPQ() || h.ColorPrimaries != "bt2020" || h.ColorSpace != "bt2020nc" || h.ColorRange != "tv" {
		t.Errorf("tags = %+v", h)
	}
	if h.MaxCLL != 1000 || h.MaxFALL != 400 {
		t.Errorf("cll = %d/%d", h.MaxCLL, h.MaxFALL)
	}
	if h.Mastering == nil {
		t.Fatal("mastering display missing")
	}
	want := "G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50)"
	if got := h.Mastering.x265MasterDisplay(); got != want {
		t.Errorf("x265 master-display = %s, want %s", got, want)
	}
	wantSVT := "G(0.265,0.69)B(0.15,0.06)R(0.68,0.32)WP(0.3127,0.329)L(1000,0.005)"
	if got := h.Mastering.svtMasterDisplay(); got != wantSVT {
		t.Errorf("svt mastering-display = %s, want %s", got, wantSVT)
	}
}

func TestParseHDRSDRAndEdgeCases(t *testing.T) {
	sdr := `{"frames":[{"color_space":"bt709","color_primaries":"bt709","color_transfer":"bt709"}]}`
	if h, err := parseHDR([]byte(sdr)); h != nil || err != nil {
		t.Errorf("SDR = %v, %v; want nil, nil", h, err)
	}
	if h, err := parseHDR([]byte(`{}`)); h != nil || err != nil {
		t.Errorf("no frames = %v, %v; want nil, nil", h, err)
	}
	if _, err := parseHDR([]byte(`not json`)); err == nil {
		t.Error("malformed JSON should error")
	}
	hlg := `{"frames":[{"color_space":"bt2020nc","color_primaries":"bt2020","color_transfer":"arib-std-b67"}]}`
	h, _ := parseHDR([]byte(hlg))
	if h == nil || h.IsPQ() || h.HasStaticMetadata() {
		t.Errorf("HLG = %+v", h)
	}
	unk := `{"frames":[{"color_space":"unknown","color_primaries":"bt2020","color_transfer":"smpte2084","color_range":"unknown"}]}`
	h, _ = parseHDR([]byte(unk))
	if h == nil || h.ColorSpace != "" || h.ColorRange != "" {
		t.Errorf("unknown tags should be dropped: %+v", h)
	}
	partial := `{"frames":[{"color_transfer":"smpte2084","side_data_list":[{"side_data_type":"Mastering display metadata","red_x":"1/2"}]}]}`
	h, _ = parseHDR([]byte(partial))
	if h == nil || h.Mastering != nil {
		t.Errorf("partial mastering metadata must be ignored: %+v", h)
	}
}

func hdr10() *HDRInfo {
	h, _ := parseHDR([]byte(hdr10FrameJSON))
	return h
}

func TestHDRArgs(t *testing.T) {
	if got := hdrArgs(EncodeSpec{Codec: "h265"}); got != nil {
		t.Errorf("SDR args = %v, want nil", got)
	}

	x265 := strings.Join(hdrArgs(EncodeSpec{Codec: "h265", HDR: hdr10()}), " ")
	for _, want := range []string{
		"-color_primaries bt2020", "-color_trc smpte2084", "-colorspace bt2020nc", "-color_range tv",
		"-x265-params hdr10=1:repeat-headers=1:master-display=G(13250,34500)",
		"max-cll=1000,400",
	} {
		if !strings.Contains(x265, want) {
			t.Errorf("x265 args missing %q in %s", want, x265)
		}
	}

	svt := strings.Join(hdrArgs(EncodeSpec{Codec: "av1", HDR: hdr10()}), " ")
	for _, want := range []string{"-svtav1-params mastering-display=G(0.265,0.69)", "content-light=1000,400"} {
		if !strings.Contains(svt, want) {
			t.Errorf("svt args missing %q in %s", want, svt)
		}
	}

	// Hardware encoders get colour tags only; static metadata rides as side data.
	hw := hdrArgs(EncodeSpec{Encoder: "hevc_nvenc", Backend: "nvenc", HDR: hdr10()})
	if slices.Contains(hw, "-x265-params") || !slices.Contains(hw, "-color_trc") {
		t.Errorf("hw args = %v", hw)
	}

	// HLG: tags only, no PQ static metadata params.
	hlg := &HDRInfo{ColorPrimaries: "bt2020", ColorTransfer: "arib-std-b67", ColorSpace: "bt2020nc"}
	if a := hdrArgs(EncodeSpec{Codec: "h265", HDR: hlg}); slices.Contains(a, "-x265-params") || !slices.Contains(a, "arib-std-b67") {
		t.Errorf("hlg args = %v", a)
	}
}

func TestBuildArgsIncludesHDR(t *testing.T) {
	args := buildArgs("in.mkv", "out.mkv", EncodeSpec{Codec: "h265", CRF: 22, HDR: hdr10()})
	if !slices.Contains(args, "-x265-params") {
		t.Errorf("buildArgs missing HDR params: %v", args)
	}
	if sdr := buildArgs("in.mkv", "out.mkv", EncodeSpec{Codec: "h265", CRF: 22}); slices.Contains(sdr, "-color_trc") {
		t.Errorf("SDR buildArgs must not tag colour: %v", sdr)
	}
}
