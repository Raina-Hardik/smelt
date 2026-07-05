package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/ffmpeg"
	"github.com/Raina-Hardik/smelt/internal/scanner"
)

// newResolvedPool returns a Pool whose once-per-run encoder resolution is
// already consumed, so tests control encoder/backend without probing.
func newResolvedPool(cfg *config.Config, encoder, backend string) *Pool {
	pool := New(cfg, nil)
	pool.resolveOnce.Do(func() {})
	pool.encoder, pool.backend = encoder, backend
	return pool
}

func stubWorkerFFmpeg(t *testing.T) {
	t.Helper()
	origRun, origAttrs, origDecode, orig10 := ffmpegRun, probeAttrs, probeDecode, probeEncoder10Bit
	t.Cleanup(func() {
		ffmpegRun, probeAttrs, probeDecode, probeEncoder10Bit = origRun, origAttrs, origDecode, orig10
	})
}

func attrsFor(pixFmt string) *ffmpeg.FileAttrs {
	return &ffmpeg.FileAttrs{VideoCodec: "hevc", Profile: "Main", PixFmt: pixFmt, BitDepth: ffmpeg.HWPipelineBitDepth(pixFmt)}
}

func TestDecideDecodeSoftwareEncodeUntouched(t *testing.T) {
	stubWorkerFFmpeg(t)
	probeAttrs = func(ctx context.Context, path string) (*ffmpeg.FileAttrs, error) {
		t.Fatal("software encode must not probe attrs")
		return nil, nil
	}
	pool := newResolvedPool(&config.Config{Workers: 1, HWDecode: "auto"}, "libx265", "")
	spec := ffmpeg.EncodeSpec{Codec: "h265", Encoder: "libx265"}
	if attrs := pool.decideDecode(context.Background(), "a.mkv", &spec); attrs != nil {
		t.Errorf("attrs = %+v, want nil", attrs)
	}
	if spec.DecodeBackend != "" || spec.SourceBitDepth != 0 {
		t.Errorf("spec changed: %+v", spec)
	}
}

func TestDecideDecodeEnablesHWDecode(t *testing.T) {
	stubWorkerFFmpeg(t)
	probeAttrs = func(ctx context.Context, path string) (*ffmpeg.FileAttrs, error) { return attrsFor("yuv420p"), nil }
	probeDecode = func(ctx context.Context, backend, src string, a *ffmpeg.FileAttrs) bool { return true }
	pool := newResolvedPool(&config.Config{Workers: 1, HWDecode: "auto"}, "hevc_vaapi", "vaapi")
	spec := ffmpeg.EncodeSpec{Codec: "h265", Encoder: "hevc_vaapi", Backend: "vaapi"}
	pool.decideDecode(context.Background(), "a.mkv", &spec)
	if spec.DecodeBackend != "vaapi" || spec.SourceBitDepth != 8 {
		t.Errorf("spec = %+v, want DecodeBackend vaapi, depth 8", spec)
	}
}

func TestDecideDecodeHWDecodeOff(t *testing.T) {
	stubWorkerFFmpeg(t)
	probeAttrs = func(ctx context.Context, path string) (*ffmpeg.FileAttrs, error) { return attrsFor("yuv420p10le"), nil }
	probeEncoder10Bit = func(ctx context.Context, enc, backend string) bool { return true }
	probeDecode = func(ctx context.Context, backend, src string, a *ffmpeg.FileAttrs) bool {
		t.Fatal("hwdecode off must not run the decode probe")
		return false
	}
	pool := newResolvedPool(&config.Config{Workers: 1, HWDecode: "off"}, "hevc_vaapi", "vaapi")
	spec := ffmpeg.EncodeSpec{Codec: "h265", Encoder: "hevc_vaapi", Backend: "vaapi"}
	pool.decideDecode(context.Background(), "a.mkv", &spec)
	if spec.DecodeBackend != "" {
		t.Errorf("DecodeBackend = %q, want software decode", spec.DecodeBackend)
	}
	if spec.SourceBitDepth != 10 {
		t.Errorf("SourceBitDepth = %d, want 10 (p010 upload still applies)", spec.SourceBitDepth)
	}
}

func TestDecideDecodeTenBitWithoutMain10GoesSoftware(t *testing.T) {
	stubWorkerFFmpeg(t)
	probeAttrs = func(ctx context.Context, path string) (*ffmpeg.FileAttrs, error) { return attrsFor("yuv420p10le"), nil }
	probeEncoder10Bit = func(ctx context.Context, enc, backend string) bool { return false }
	pool := newResolvedPool(&config.Config{Workers: 1, HWDecode: "auto"}, "hevc_nvenc", "nvenc")
	spec := ffmpeg.EncodeSpec{Codec: "h265", Encoder: "hevc_nvenc", Backend: "nvenc", Preset: "p5"}
	pool.decideDecode(context.Background(), "a.mkv", &spec)
	if spec.Backend != "" || spec.Encoder != "libx265" || spec.DecodeBackend != "" {
		t.Errorf("spec = %+v, want full software pipeline", spec)
	}
	if spec.Preset != "medium" {
		t.Errorf("Preset = %q, want snap to software default (p5 is nvenc-only)", spec.Preset)
	}
}

func TestDecideDecodeExoticShapesGoSoftware(t *testing.T) {
	stubWorkerFFmpeg(t)
	for _, pf := range []string{"yuv422p10le", "yuv444p", "yuv420p12le"} {
		probeAttrs = func(ctx context.Context, path string) (*ffmpeg.FileAttrs, error) { return attrsFor(pf), nil }
		pool := newResolvedPool(&config.Config{Workers: 1, HWDecode: "auto"}, "hevc_vaapi", "vaapi")
		spec := ffmpeg.EncodeSpec{Codec: "h265", Encoder: "hevc_vaapi", Backend: "vaapi"}
		pool.decideDecode(context.Background(), "a.mkv", &spec)
		if spec.Backend != "" || spec.Encoder != "libx265" {
			t.Errorf("%s: spec = %+v, want software pipeline", pf, spec)
		}
	}
}

func TestDecideDecodeAttrsProbeFailureKeepsV020Behavior(t *testing.T) {
	stubWorkerFFmpeg(t)
	probeAttrs = func(ctx context.Context, path string) (*ffmpeg.FileAttrs, error) {
		return nil, errors.New("unreadable")
	}
	pool := newResolvedPool(&config.Config{Workers: 1, HWDecode: "auto"}, "hevc_vaapi", "vaapi")
	spec := ffmpeg.EncodeSpec{Codec: "h265", Encoder: "hevc_vaapi", Backend: "vaapi"}
	pool.decideDecode(context.Background(), "a.mkv", &spec)
	if spec.Backend != "vaapi" || spec.DecodeBackend != "" || spec.SourceBitDepth != 0 {
		t.Errorf("spec = %+v, want hw encode + software decode, untouched", spec)
	}
}

// transcodeFile drives Pool.transcode against a stubbed ffmpegRun in a temp
// dir, returning the error and how many times ffmpeg "ran".
func transcodeFile(t *testing.T, cfg *config.Config, run func(call int, spec ffmpeg.EncodeSpec) error) (int, error) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "in.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Src = dir
	if cfg.Suffix == "" {
		cfg.Suffix = ".smelt"
	}

	var mu sync.Mutex
	calls := 0
	ffmpegRun = func(ctx context.Context, s, dst string, spec ffmpeg.EncodeSpec, onProgress func(ffmpeg.ProgressEvent)) error {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if err := run(n, spec); err != nil {
			return err
		}
		return os.WriteFile(dst, []byte("out"), 0o644)
	}
	pool := newResolvedPool(cfg, "hevc_vaapi", "vaapi")
	err := pool.transcode(context.Background(), scanner.MediaFile{Path: src, RelPath: "in.mkv"}, nil)
	return calls, err
}

func hwDecodeStubs(t *testing.T) {
	stubWorkerFFmpeg(t)
	probeAttrs = func(ctx context.Context, path string) (*ffmpeg.FileAttrs, error) { return attrsFor("yuv420p"), nil }
	probeDecode = func(ctx context.Context, backend, src string, a *ffmpeg.FileAttrs) bool { return true }
}

func TestRetrySucceedsWithSoftwareDecode(t *testing.T) {
	hwDecodeStubs(t)
	calls, err := transcodeFile(t, &config.Config{Workers: 1, HWDecode: "auto", Codec: "h265"}, func(call int, spec ffmpeg.EncodeSpec) error {
		if call == 1 {
			if spec.DecodeBackend != "vaapi" {
				t.Errorf("first attempt DecodeBackend = %q, want vaapi", spec.DecodeBackend)
			}
			return &ffmpeg.ExecError{FilePath: "in.mkv", ExitCode: 1, Stderr: "hw decode blew up"}
		}
		if spec.DecodeBackend != "" {
			t.Errorf("retry DecodeBackend = %q, want software", spec.DecodeBackend)
		}
		return nil
	})
	if err != nil {
		t.Errorf("retry should succeed, got %v", err)
	}
	if calls != 2 {
		t.Errorf("ffmpeg ran %d times, want 2", calls)
	}
}

func TestRetryFailureCountsOnce(t *testing.T) {
	hwDecodeStubs(t)
	calls, err := transcodeFile(t, &config.Config{Workers: 1, HWDecode: "auto", Codec: "h265"}, func(call int, spec ffmpeg.EncodeSpec) error {
		return &ffmpeg.ExecError{FilePath: "in.mkv", ExitCode: 1, Stderr: "still broken"}
	})
	if err == nil {
		t.Error("both attempts failed; want error")
	}
	if calls != 2 {
		t.Errorf("ffmpeg ran %d times, want 2 (one retry max)", calls)
	}
}

func TestNoRetryWithSoftwareDecode(t *testing.T) {
	hwDecodeStubs(t)
	probeDecode = func(ctx context.Context, backend, src string, a *ffmpeg.FileAttrs) bool { return false }
	calls, _ := transcodeFile(t, &config.Config{Workers: 1, HWDecode: "auto", Codec: "h265"}, func(call int, spec ffmpeg.EncodeSpec) error {
		return &ffmpeg.ExecError{FilePath: "in.mkv", ExitCode: 1}
	})
	if calls != 1 {
		t.Errorf("ffmpeg ran %d times, want 1 (software decode never retries)", calls)
	}
}

func TestNoRetryOnOSError(t *testing.T) {
	hwDecodeStubs(t)
	calls, _ := transcodeFile(t, &config.Config{Workers: 1, HWDecode: "auto", Codec: "h265"}, func(call int, spec ffmpeg.EncodeSpec) error {
		return &ffmpeg.OSError{FilePath: "in.mkv", Err: errors.New("fork failed")}
	})
	if calls != 1 {
		t.Errorf("ffmpeg ran %d times, want 1 (OSError never retries)", calls)
	}
}

func TestNoRetryAfterContextCancel(t *testing.T) {
	hwDecodeStubs(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "in.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	ffmpegRun = func(c context.Context, s, dst string, spec ffmpeg.EncodeSpec, onProgress func(ffmpeg.ProgressEvent)) error {
		calls++
		cancel() // ffmpeg was killed by cancellation mid-file
		return &ffmpeg.ExecError{FilePath: s, ExitCode: -1}
	}
	pool := newResolvedPool(&config.Config{Workers: 1, HWDecode: "auto", Codec: "h265", Src: dir, Suffix: ".smelt"}, "hevc_vaapi", "vaapi")
	if err := pool.transcode(ctx, scanner.MediaFile{Path: src, RelPath: "in.mkv"}, nil); err == nil {
		t.Error("cancelled transcode should fail")
	}
	if calls != 1 {
		t.Errorf("ffmpeg ran %d times, want 1 (no retry after cancel)", calls)
	}
}

func TestResourceProfileHWDecodeLabel(t *testing.T) {
	got := BuildResourceProfile("hevc_vaapi", "vaapi", 0, "auto").DecodeLabel()
	want := "hw: vaapi (per-file, may fall back)"
	if got != want {
		t.Errorf("DecodeLabel = %q, want %q", got, want)
	}
}
