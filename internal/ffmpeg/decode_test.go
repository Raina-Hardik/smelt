package ffmpeg

import (
	"context"
	"errors"
	"testing"
)

func resetDecodeCache() {
	decodeMu.Lock()
	decodeCache = map[decodeShape]bool{}
	decodeMu.Unlock()
}

func stubDecodeProbe(t *testing.T, fn func(ctx context.Context, args ...string) error) {
	t.Helper()
	orig := runDecodeProbe
	runDecodeProbe = fn
	t.Cleanup(func() { runDecodeProbe = orig; resetDecodeCache() })
	resetDecodeCache()
}

func hevc10Attrs() *FileAttrs {
	return &FileAttrs{VideoCodec: "hevc", Profile: "Main 10", PixFmt: "yuv420p10le", BitDepth: 10}
}

func TestProbeDecodeCachesPositivePerShape(t *testing.T) {
	calls := 0
	stubDecodeProbe(t, func(ctx context.Context, args ...string) error { calls++; return nil })

	if !ProbeDecode(context.Background(), "vaapi", "a.mkv", hevc10Attrs()) {
		t.Fatal("expected positive probe")
	}
	// Different file, same shape: must hit the cache.
	if !ProbeDecode(context.Background(), "vaapi", "b.mkv", hevc10Attrs()) {
		t.Fatal("expected cached positive")
	}
	if calls != 1 {
		t.Errorf("probe ran %d times, want 1 (shape-keyed cache)", calls)
	}
}

func TestProbeDecodeCachesCompletedNegative(t *testing.T) {
	calls := 0
	stubDecodeProbe(t, func(ctx context.Context, args ...string) error { calls++; return errors.New("no decode block") })

	if ProbeDecode(context.Background(), "vaapi", "a.mkv", hevc10Attrs()) {
		t.Fatal("expected negative probe")
	}
	if ProbeDecode(context.Background(), "vaapi", "b.mkv", hevc10Attrs()) {
		t.Fatal("expected cached negative")
	}
	if calls != 1 {
		t.Errorf("probe ran %d times, want 1 (completed negatives are decisive)", calls)
	}
}

func TestProbeDecodeCancelNotCached(t *testing.T) {
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	stubDecodeProbe(t, func(c context.Context, args ...string) error {
		calls++
		cancel() // simulate the parent being cancelled mid-probe
		return errors.New("killed")
	})

	if ProbeDecode(ctx, "vaapi", "a.mkv", hevc10Attrs()) {
		t.Fatal("cancelled probe should report false")
	}
	if ProbeDecode(context.Background(), "vaapi", "a.mkv", hevc10Attrs()) {
		t.Fatal("stub still fails; want fresh probe result")
	}
	if calls != 2 {
		t.Errorf("probe ran %d times, want 2 (cancel is not evidence, not cached)", calls)
	}
}

func TestProbeDecodeDistinguishesShapes(t *testing.T) {
	calls := 0
	stubDecodeProbe(t, func(ctx context.Context, args ...string) error { calls++; return nil })

	ProbeDecode(context.Background(), "vaapi", "a.mkv", hevc10Attrs())
	ProbeDecode(context.Background(), "vaapi", "a.mkv", &FileAttrs{VideoCodec: "h264", Profile: "High", PixFmt: "yuv420p"})
	ProbeDecode(context.Background(), "nvenc", "a.mkv", hevc10Attrs()) // same shape, other backend
	if calls != 3 {
		t.Errorf("probe ran %d times, want 3 (backend+codec+profile+pix_fmt key)", calls)
	}
}

func TestEvictDecodeCacheForcesReprobe(t *testing.T) {
	calls := 0
	stubDecodeProbe(t, func(ctx context.Context, args ...string) error { calls++; return nil })

	ProbeDecode(context.Background(), "vaapi", "a.mkv", hevc10Attrs())
	EvictDecodeCache("vaapi", hevc10Attrs())
	ProbeDecode(context.Background(), "vaapi", "b.mkv", hevc10Attrs())
	if calls != 2 {
		t.Errorf("probe ran %d times, want 2 (eviction forces re-probe)", calls)
	}
}

func TestProbeDecodeRefusesEncodeOnlyBackends(t *testing.T) {
	stubDecodeProbe(t, func(ctx context.Context, args ...string) error {
		t.Fatal("encode-only backends must not spawn a probe")
		return nil
	})
	for _, b := range []string{"amf", "videotoolbox", ""} {
		if ProbeDecode(context.Background(), b, "a.mkv", hevc10Attrs()) {
			t.Errorf("backend %q: decode must be software this release", b)
		}
	}
}
