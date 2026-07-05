package ffmpeg

import (
	"reflect"
	"testing"
)

// TestDecodeArgMatrix locks the pre-input/filter/rate-control arg shapes for
// every (backend × decode-mode × bit-depth) cell — the regression surface for
// the hardware pipeline. Shapes validated on real hardware (VAAPI + QSV on an
// Arrow Lake iGPU, CUDA on an RTX 5060); see docs/ARCHITECTURE.md.
func TestDecodeArgMatrix(t *testing.T) {
	va := vaapiDevice()
	cases := []struct {
		name    string
		spec    EncodeSpec
		wantPre []string
		wantVF  []string
		wantRC  []string
	}{
		{
			"vaapi hw-decode 8-bit",
			EncodeSpec{Encoder: "hevc_vaapi", Backend: "vaapi", DecodeBackend: "vaapi", SourceBitDepth: 8, CRF: 24},
			[]string{"-vaapi_device", va, "-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi"},
			nil,
			[]string{"-c:v", "hevc_vaapi", "-rc_mode", "CQP", "-qp", "24"},
		},
		{
			"vaapi hw-decode 10-bit",
			EncodeSpec{Encoder: "hevc_vaapi", Backend: "vaapi", DecodeBackend: "vaapi", SourceBitDepth: 10, CRF: 24},
			[]string{"-vaapi_device", va, "-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi"},
			nil,
			[]string{"-c:v", "hevc_vaapi", "-rc_mode", "CQP", "-qp", "24", "-profile:v", "main10"},
		},
		{
			"vaapi sw-decode 8-bit uploads nv12",
			EncodeSpec{Encoder: "hevc_vaapi", Backend: "vaapi", SourceBitDepth: 8, CRF: 24, DecodeThreads: 2},
			[]string{"-threads", "2", "-vaapi_device", va},
			[]string{"-vf", "format=nv12,hwupload"},
			[]string{"-c:v", "hevc_vaapi", "-rc_mode", "CQP", "-qp", "24"},
		},
		{
			"vaapi sw-decode 10-bit uploads p010 with main10",
			EncodeSpec{Encoder: "hevc_vaapi", Backend: "vaapi", SourceBitDepth: 10, CRF: 24},
			[]string{"-vaapi_device", va},
			[]string{"-vf", "format=p010,hwupload"},
			[]string{"-c:v", "hevc_vaapi", "-rc_mode", "CQP", "-qp", "24", "-profile:v", "main10"},
		},
		{
			"qsv hw-decode 8-bit",
			EncodeSpec{Encoder: "hevc_qsv", Backend: "qsv", DecodeBackend: "qsv", SourceBitDepth: 8, CRF: 23},
			[]string{"-init_hw_device", "qsv=qsv:hw_any", "-hwaccel", "qsv", "-hwaccel_output_format", "qsv"},
			nil,
			[]string{"-c:v", "hevc_qsv", "-q:v", "23"},
		},
		{
			"qsv hw-decode 10-bit",
			EncodeSpec{Encoder: "hevc_qsv", Backend: "qsv", DecodeBackend: "qsv", SourceBitDepth: 10, CRF: 23},
			[]string{"-init_hw_device", "qsv=qsv:hw_any", "-hwaccel", "qsv", "-hwaccel_output_format", "qsv"},
			nil,
			[]string{"-c:v", "hevc_qsv", "-q:v", "23", "-profile:v", "main10"},
		},
		{
			"qsv sw-decode keeps device init only",
			EncodeSpec{Encoder: "hevc_qsv", Backend: "qsv", CRF: 23, DecodeThreads: 4},
			[]string{"-threads", "4", "-init_hw_device", "qsv=qsv:hw_any"},
			nil,
			[]string{"-c:v", "hevc_qsv", "-q:v", "23"},
		},
		{
			"nvenc hw-decode 8-bit (cuda)",
			EncodeSpec{Encoder: "hevc_nvenc", Backend: "nvenc", DecodeBackend: "nvenc", SourceBitDepth: 8, CRF: 23},
			[]string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"},
			nil,
			[]string{"-c:v", "hevc_nvenc", "-rc", "vbr", "-cq", "23"},
		},
		{
			"nvenc hw-decode 10-bit",
			EncodeSpec{Encoder: "hevc_nvenc", Backend: "nvenc", DecodeBackend: "nvenc", SourceBitDepth: 10, CRF: 23},
			[]string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"},
			nil,
			[]string{"-c:v", "hevc_nvenc", "-rc", "vbr", "-cq", "23", "-profile:v", "main10"},
		},
		{
			"nvenc sw-decode has no pre-input device args",
			EncodeSpec{Encoder: "hevc_nvenc", Backend: "nvenc", CRF: 23},
			nil,
			nil,
			[]string{"-c:v", "hevc_nvenc", "-rc", "vbr", "-cq", "23"},
		},
		{
			"av1 hw-decode 10-bit takes no profile flag",
			EncodeSpec{Encoder: "av1_nvenc", Backend: "nvenc", DecodeBackend: "nvenc", SourceBitDepth: 10, CRF: 30},
			[]string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"},
			nil,
			[]string{"-c:v", "av1_nvenc", "-rc", "vbr", "-cq", "30"},
		},
		{
			"software pipeline unchanged (10-bit source)",
			EncodeSpec{Codec: "h265", CRF: 23, SourceBitDepth: 10, DecodeThreads: 2},
			[]string{"-threads", "2"},
			nil,
			[]string{"-c:v", "libx265", "-crf", "23"},
		},
	}
	for _, c := range cases {
		if got := preInputArgs(c.spec); !reflect.DeepEqual(got, c.wantPre) {
			t.Errorf("%s: preInputArgs = %v, want %v", c.name, got, c.wantPre)
		}
		if got := videoFilterArgs(c.spec); !reflect.DeepEqual(got, c.wantVF) {
			t.Errorf("%s: videoFilterArgs = %v, want %v", c.name, got, c.wantVF)
		}
		if got := rateControlArgs(c.spec); !reflect.DeepEqual(got, c.wantRC) {
			t.Errorf("%s: rateControlArgs = %v, want %v", c.name, got, c.wantRC)
		}
		// Same-device invariant: DecodeBackend is "" or equals Backend. The
		// matrix above never violates it; this guards future cell additions.
		if db := c.spec.DecodeBackend; db != "" && db != c.spec.Backend {
			t.Errorf("%s: spec violates same-device invariant: decode %q vs encode %q", c.name, db, c.spec.Backend)
		}
	}
}

// TestDecodeThreadsOmittedWithHWDecode pins the --decode-threads interaction:
// the cap is dropped from the command while hardware decode is active.
func TestDecodeThreadsOmittedWithHWDecode(t *testing.T) {
	spec := EncodeSpec{Encoder: "hevc_vaapi", Backend: "vaapi", DecodeBackend: "vaapi", DecodeThreads: 4, CRF: 24}
	for _, a := range buildArgs("in.mkv", "out.mkv", spec) {
		if a == "-threads" {
			t.Fatalf("hw decode active: -threads must be omitted, got %v", buildArgs("in.mkv", "out.mkv", spec))
		}
	}
}
