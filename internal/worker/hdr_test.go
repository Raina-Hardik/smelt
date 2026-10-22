package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/ffmpeg"
)

func TestTranscodePassesHDRToSpec(t *testing.T) {
	stubWorkerFFmpeg(t)
	probeAttrs = func(ctx context.Context, path string) (*ffmpeg.FileAttrs, error) { return attrsFor("yuv420p10le"), nil }
	probeEncoder10Bit = func(ctx context.Context, enc, backend string) bool { return true }
	probeDecode = func(ctx context.Context, backend, src string, a *ffmpeg.FileAttrs) bool { return false }
	want := &ffmpeg.HDRInfo{ColorPrimaries: "bt2020", ColorTransfer: "smpte2084", ColorSpace: "bt2020nc", MaxCLL: 1000}
	probeHDR = func(ctx context.Context, path string) (*ffmpeg.HDRInfo, error) { return want, nil }

	var got *ffmpeg.HDRInfo
	_, err := transcodeFile(t, &config.Config{Workers: 1, HWDecode: "auto", Codec: "h265"}, func(call int, spec ffmpeg.EncodeSpec) error {
		got = spec.HDR
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("spec.HDR = %+v, want %+v", got, want)
	}
}

func TestTranscodeSDRAndProbeFailureLeaveHDRNil(t *testing.T) {
	for name, probe := range map[string]func(context.Context, string) (*ffmpeg.HDRInfo, error){
		"sdr":   func(context.Context, string) (*ffmpeg.HDRInfo, error) { return nil, nil },
		"error": func(context.Context, string) (*ffmpeg.HDRInfo, error) { return nil, errors.New("ffprobe failed") },
	} {
		t.Run(name, func(t *testing.T) {
			hwDecodeStubs(t)
			probeHDR = probe
			_, err := transcodeFile(t, &config.Config{Workers: 1, HWDecode: "auto", Codec: "h265"}, func(call int, spec ffmpeg.EncodeSpec) error {
				if spec.HDR != nil {
					t.Errorf("spec.HDR = %+v, want nil", spec.HDR)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTranscodePassesChunkSettingsToSpec(t *testing.T) {
	hwDecodeStubs(t)
	cfg := &config.Config{Workers: 1, HWDecode: "auto", Codec: "h265", ChunkDuration: 10 * time.Minute, ChunkCooldown: 30 * time.Second}
	_, err := transcodeFile(t, cfg, func(call int, spec ffmpeg.EncodeSpec) error {
		if spec.ChunkDuration != 10*time.Minute || spec.ChunkCooldown != 30*time.Second {
			t.Errorf("spec chunk = %v / %v", spec.ChunkDuration, spec.ChunkCooldown)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Segment and work files must never be picked up as new sources.
func TestIsOwnOutputCoversChunkFiles(t *testing.T) {
	for _, name := range []string{"seg00001.transcoded.mkv", "enc00001.transcoded.mkv", "movie.transcoded.mkv"} {
		if !isOwnOutput("/m/movie.transcoded.chunks/"+name, ".smelt") {
			t.Errorf("%s should be recognised as smelt's own output", name)
		}
	}
}
