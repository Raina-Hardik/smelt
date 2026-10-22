package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/ffmpeg"
	"github.com/Raina-Hardik/smelt/internal/scanner"
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
	for _, name := range []string{"enc00001.transcoded.mkv", "movie.transcoded.mkv"} {
		if !isOwnOutput("/m/movie.transcoded.mkv.chunks/"+name, ".smelt") {
			t.Errorf("%s should be recognised as smelt's own output", name)
		}
	}
}

// --ffmpeg-arg values that change the timeline cannot be applied per chunk;
// chunking must switch itself off rather than produce a different file.
func TestTranscodeDisablesChunkingForTimelineArgs(t *testing.T) {
	cases := []struct {
		name      string
		extra     []string
		wantChunk bool
	}{
		{"scale only", []string{"-vf", "scale=1280:-2"}, true},
		{"trim", []string{"-t", "20"}, false},
		{"fps filter", []string{"-vf", "fps=12"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hwDecodeStubs(t)
			cfg := &config.Config{Workers: 1, HWDecode: "auto", Codec: "h265", ExtraArgs: c.extra,
				ChunkDuration: 10 * time.Minute, ChunkCooldown: time.Second}
			_, err := transcodeFile(t, cfg, func(call int, spec ffmpeg.EncodeSpec) error {
				if got := spec.ChunkDuration > 0; got != c.wantChunk {
					t.Errorf("chunking = %v, want %v (ChunkDuration=%v)", got, c.wantChunk, spec.ChunkDuration)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A failed join must not trigger the hw-decode retry: it is not a decode
// problem and a retry would redo every chunk (and every cooldown).
func TestJoinFailureDoesNotRetry(t *testing.T) {
	hwDecodeStubs(t)
	calls, err := transcodeFile(t, &config.Config{Workers: 1, HWDecode: "auto", Codec: "h265"}, func(call int, spec ffmpeg.EncodeSpec) error {
		return &ffmpeg.JoinError{ExecError: &ffmpeg.ExecError{FilePath: "in.mkv", ExitCode: 1, Stderr: "join failed"}}
	})
	if err == nil {
		t.Error("expected the join error to surface")
	}
	if calls != 1 {
		t.Errorf("ffmpeg ran %d times, want 1 (no retry for a join failure)", calls)
	}
}

// End to end through Pool.transcode: with --inplace, an output that fails
// verification must leave the original byte-for-byte intact and remove the
// temporary file.
func TestInplaceKeepsOriginalWhenOutputFailsVerification(t *testing.T) {
	for name, healthy := range map[string]bool{"bad output": false, "good output": true} {
		t.Run(name, func(t *testing.T) {
			hwDecodeStubs(t)
			probeHealth = func(_ context.Context, path string) (*ffmpeg.FileHealth, error) {
				return &ffmpeg.FileHealth{HasVideo: healthy}, nil
			}
			dir := t.TempDir()
			src := filepath.Join(dir, "in.mkv")
			if err := os.WriteFile(src, []byte("ORIGINAL"), 0o644); err != nil {
				t.Fatal(err)
			}
			ffmpegRun = func(_ context.Context, _, dst string, _ ffmpeg.EncodeSpec, _ func(ffmpeg.ProgressEvent)) error {
				return os.WriteFile(dst, []byte("ENCODED"), 0o644)
			}
			cfg := &config.Config{Workers: 1, HWDecode: "auto", Codec: "h265", InPlace: true, Src: dir}
			pool := newResolvedPool(cfg, "hevc_vaapi", "vaapi")
			err := pool.transcode(context.Background(), scanner.MediaFile{Path: src, RelPath: "in.mkv"}, nil)

			got, _ := os.ReadFile(src)
			if healthy {
				if err != nil || string(got) != "ENCODED" {
					t.Errorf("healthy output should replace the original: err=%v content=%q", err, got)
				}
				return
			}
			if err == nil {
				t.Error("expected a refusal error")
			}
			if string(got) != "ORIGINAL" {
				t.Errorf("original was modified: %q", got)
			}
			if left, _ := filepath.Glob(filepath.Join(dir, "*.transcoded.*")); len(left) != 0 {
				t.Errorf("temporary output left behind: %v", left)
			}
		})
	}
}
