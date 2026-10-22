//go:build integration

package ffmpeg

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestProbeVideoCodecIntegration generates a tiny real h264 file and probes it.
// Requires ffmpeg and ffprobe on $PATH (run with: go test -tags integration ./...).
func TestProbeVideoCodecIntegration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.mp4")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=s=64x64:d=1", "-c:v", "libx264", "-t", "1", path)
	if err := gen.Run(); err != nil {
		t.Skipf("cannot generate test media (ffmpeg unavailable?): %v", err)
	}

	got, err := ProbeVideoCodec(context.Background(), path)
	if err != nil {
		t.Fatalf("ProbeVideoCodec: %v", err)
	}
	if got != "h264" {
		t.Errorf("ProbeVideoCodec = %q, want h264", got)
	}
}

// TestProbeDolbyVisionNegative checks that a plain (non-DV) h264 sample is not
// flagged. Positive detection (an actual DOVI configuration record) isn't
// covered here — fabricating a valid DV bitstream is out of scope for a unit
// test; ProbeDolbyVision's string match was verified manually against a real
// DV remux (see the field report this flag was added for).
func TestProbeDolbyVisionNegative(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.mp4")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=s=64x64:d=1", "-c:v", "libx264", "-t", "1", path)
	if err := gen.Run(); err != nil {
		t.Skipf("cannot generate test media (ffmpeg unavailable?): %v", err)
	}

	isDV, err := ProbeDolbyVision(context.Background(), path)
	if err != nil {
		t.Fatalf("ProbeDolbyVision: %v", err)
	}
	if isDV {
		t.Error("ProbeDolbyVision = true for a plain h264 sample, want false")
	}
}

// TestHDR10RoundTripIntegration encodes an HDR10 source through Run for each
// software encoder and verifies colour tags and static metadata survive.
//
// Note: ffmpeg 8.x already carries this metadata through plain libx265 and
// SVT-AV1 re-encodes, so this guards the end-to-end result (and older builds,
// where hdrArgs does the work), not hdrArgs in isolation. The unit tests in
// hdr_test.go are what pin the exact arguments smelt emits.
func TestHDR10RoundTripIntegration(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=s=320x240:d=1:r=24", "-f", "lavfi", "-i", "anullsrc=d=1",
		"-pix_fmt", "yuv420p10le", "-c:v", "libx265", "-c:a", "aac", "-x265-params",
		"hdr10=1:repeat-headers=1:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:"+
			"master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50):max-cll=1000,400:log-level=error",
		src)
	if err := gen.Run(); err != nil {
		t.Skipf("cannot generate HDR10 media (libx265 unavailable?): %v", err)
	}
	ctx := context.Background()
	in, err := ProbeHDR(ctx, src)
	if err != nil || in == nil || !in.IsPQ() {
		t.Fatalf("source ProbeHDR = %+v, %v", in, err)
	}

	for _, codec := range []string{"h265", "av1"} {
		dst := filepath.Join(dir, codec+".mkv")
		spec := EncodeSpec{Codec: codec, CRF: 30, Preset: "ultrafast", HDR: in}
		if err := Run(ctx, src, dst, spec, nil); err != nil {
			t.Fatalf("%s encode: %v", codec, err)
		}
		out, err := ProbeHDR(ctx, dst)
		if err != nil || out == nil {
			t.Fatalf("%s: ProbeHDR = %+v, %v", codec, out, err)
		}
		if !out.IsPQ() || out.ColorPrimaries != "bt2020" || out.ColorSpace != "bt2020nc" {
			t.Errorf("%s: colour tags lost: %+v", codec, out)
		}
		if out.MaxCLL != 1000 || out.MaxFALL != 400 {
			t.Errorf("%s: content light level = %d/%d", codec, out.MaxCLL, out.MaxFALL)
		}
		if out.Mastering == nil || out.Mastering.x265MasterDisplay() != in.Mastering.x265MasterDisplay() {
			t.Errorf("%s: mastering display = %+v, want %+v", codec, out.Mastering, in.Mastering)
		}
	}
}

// TestRunAudiolessSourceIntegration: a source with no audio stream must
// transcode (the audio map is optional) rather than fail on `-map 0:a`.
func TestRunAudiolessSourceIntegration(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "silent.mkv")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=s=64x64:d=1", "-c:v", "libx264", src)
	if err := gen.Run(); err != nil {
		t.Skipf("cannot generate test media: %v", err)
	}
	dst := filepath.Join(dir, "out.mkv")
	if err := Run(context.Background(), src, dst, EncodeSpec{Codec: "h264", CRF: 30, Preset: "ultrafast"}, nil); err != nil {
		t.Fatalf("Run on audio-less source: %v", err)
	}
}

// TestRunChunkedIntegration encodes a 12 s source in ~4 s chunks and checks
// the joined output: right duration and frame count, audio kept, work dir gone.
func TestRunChunkedIntegration(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=s=160x120:d=12:r=24", "-f", "lavfi", "-i", "sine=d=12",
		"-c:v", "libx264", "-g", "24", "-keyint_min", "24", "-sc_threshold", "0", "-c:a", "aac", src)
	if err := gen.Run(); err != nil {
		t.Skipf("cannot generate test media: %v", err)
	}
	dst := filepath.Join(dir, "src.transcoded.mkv")
	spec := EncodeSpec{
		Codec: "h265", CRF: 30, Preset: "ultrafast", SubtitleMode: "copy",
		ChunkDuration: 4 * time.Second, ChunkCooldown: 50 * time.Millisecond,
	}
	var last ProgressEvent
	if err := Run(context.Background(), src, dst, spec, func(ev ProgressEvent) { last = ev }); err != nil {
		t.Fatalf("chunked Run: %v", err)
	}
	if last.Percent != 1 {
		t.Errorf("final progress = %v, want 1", last.Percent)
	}
	if _, err := os.Stat(chunkDir(dst)); !os.IsNotExist(err) {
		t.Errorf("work dir %s should be removed (stat err = %v)", chunkDir(dst), err)
	}

	out, err := exec.Command("ffprobe", "-v", "error", "-count_frames",
		"-show_entries", "stream=codec_type,codec_name,nb_read_frames", "-of", "default=noprint_wrappers=1", dst).Output()
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"codec_name=hevc", "nb_read_frames=288", "codec_type=audio"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
	d, err := probeDuration(context.Background(), dst)
	if err != nil || d < 11*time.Second || d > 13*time.Second {
		t.Errorf("duration = %v, %v; want ~12s", d, err)
	}
}

// ffmpegCallLog puts a logging wrapper in front of ffmpeg on PATH and returns
// a function reporting how many times a given token appeared in its argv.
func ffmpegCallLog(t *testing.T) func(token string) int {
	t.Helper()
	real, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not found")
	}
	dir := t.TempDir()
	logf := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\necho \"$@\" >> " + logf + "\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func(token string) int {
		b, _ := os.ReadFile(logf)
		n := 0
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, token) {
				n++
			}
		}
		return n
	}
}

func makeGOPClip(t *testing.T, dir string, secs int) string {
	t.Helper()
	src := filepath.Join(dir, "src.mkv")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=s=160x120:d="+strconv.Itoa(secs)+":r=25", "-f", "lavfi", "-i", "sine=d="+strconv.Itoa(secs),
		"-c:v", "libx264", "-g", "25", "-keyint_min", "25", "-sc_threshold", "0", "-c:a", "aac", src)
	if err := gen.Run(); err != nil {
		t.Skipf("cannot generate test media: %v", err)
	}
	return src
}

// An interrupted chunked run keeps its finished chunks; the next run resumes
// from them (re-encoding only what is missing) and still produces exactly the
// source's frames. Changed settings must not reuse the old chunks.
func TestChunkedResumeAfterInterruptIntegration(t *testing.T) {
	dir := t.TempDir()
	src := makeGOPClip(t, dir, 40)
	calls := ffmpegCallLog(t)
	dst := filepath.Join(dir, "src.transcoded.mkv")
	spec := EncodeSpec{Codec: "h265", CRF: 32, Preset: "ultrafast", SubtitleMode: "drop", ChunkDuration: 10 * time.Second}

	// Run 1: cooldown is long, so cancelling mid-cooldown leaves chunk 1 done.
	slow := spec
	slow.ChunkCooldown = 30 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := Run(ctx, src, dst, slow, nil); err == nil {
		t.Fatal("expected the run to be interrupted")
	}
	enc, _ := filepath.Glob(filepath.Join(chunkDir(dst), "enc*.transcoded.mkv"))
	if len(enc) == 0 {
		t.Fatalf("an interrupted run with finished chunks must keep them in %s", chunkDir(dst))
	}
	if left, _ := filepath.Glob(filepath.Join(chunkDir(dst), "*.part.*")); len(left) != 0 {
		t.Errorf("in-flight part files must not outlive the run: %v", left)
	}
	before := calls("-ss ")

	// Run 2: resumes. Finished chunks are not re-encoded.
	if err := Run(context.Background(), src, dst, spec, nil); err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	encoded := calls("-ss ") - before
	if encoded >= 4 || encoded < 1 {
		t.Errorf("resumed run encoded %d chunks, want fewer than the 4 total (and at least 1)", encoded)
	}
	if a, b := framesOf(t, src), framesOf(t, dst); a != b {
		t.Errorf("frames after resume: source=%s output=%s", a, b)
	}
	if _, err := os.Stat(chunkDir(dst)); !os.IsNotExist(err) {
		t.Errorf("work dir must be removed after success (err=%v)", err)
	}

	// Different settings after an interrupt: the stale chunks are discarded.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel2()
	_ = os.Remove(dst)
	if err := Run(ctx2, src, dst, slow, nil); err == nil {
		t.Fatal("expected interruption")
	}
	changed := spec
	changed.CRF = 40
	before = calls("-ss ")
	if err := Run(context.Background(), src, dst, changed, nil); err != nil {
		t.Fatal(err)
	}
	if got := calls("-ss ") - before; got != 4 {
		t.Errorf("changed CRF must re-encode every chunk, encoded %d of 4", got)
	}
}

// A run cancelled before any chunk finished has nothing to resume, so it must
// not leave a work directory behind.
func TestChunkedCancelBeforeFirstChunkLeavesNothingIntegration(t *testing.T) {
	dir := t.TempDir()
	src := makeGOPClip(t, dir, 40)
	dst := filepath.Join(dir, "src.transcoded.mkv")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	spec := EncodeSpec{Codec: "h265", CRF: 32, Preset: "ultrafast", ChunkDuration: 10 * time.Second}
	if err := Run(ctx, src, dst, spec, nil); err == nil {
		t.Fatal("expected an error from a cancelled context")
	}
	if _, err := os.Stat(chunkDir(dst)); !os.IsNotExist(err) {
		t.Errorf("nothing finished, so nothing should be kept (stat err = %v)", err)
	}
}

// Container, metadata and audio --ffmpeg-arg values take effect in chunked
// mode (they are applied once, in the join).
func TestChunkedAppliesOutputExtraArgsIntegration(t *testing.T) {
	dir := t.TempDir()
	src := makeGOPClip(t, dir, 30)
	dst := filepath.Join(dir, "src.transcoded.mkv")
	spec := EncodeSpec{Codec: "h265", CRF: 32, Preset: "ultrafast", SubtitleMode: "copy", AudioCodec: "aac",
		ChunkDuration: 10 * time.Second,
		ExtraArgs:     []string{"-vf", "scale=96:-2", "-metadata", "title=FromExtraArgs", "-b:a", "48k"}}
	if err := Run(context.Background(), src, dst, spec, nil); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format_tags=title:stream=codec_type,width",
		"-of", "default=noprint_wrappers=1", dst).Output()
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "TAG:title=FromExtraArgs") {
		t.Errorf("-metadata from --ffmpeg-arg must reach the output:\n%s", s)
	}
	if !strings.Contains(s, "width=96") {
		t.Errorf("-vf from --ffmpeg-arg must apply to every chunk:\n%s", s)
	}
}

// HDR10 signalling must survive the split / per-segment encode / join path.
func TestRunChunkedPreservesHDR10Integration(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "hdr.mkv")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=s=160x120:d=8:r=24", "-pix_fmt", "yuv420p10le",
		"-c:v", "libx265", "-x265-params",
		"hdr10=1:repeat-headers=1:keyint=24:min-keyint=24:scenecut=0:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:"+
			"master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50):max-cll=1000,400:log-level=error",
		src)
	if err := gen.Run(); err != nil {
		t.Skipf("cannot generate HDR10 media: %v", err)
	}
	ctx := context.Background()
	in, err := ProbeHDR(ctx, src)
	if err != nil || in == nil {
		t.Fatalf("ProbeHDR(src) = %+v, %v", in, err)
	}
	dst := filepath.Join(dir, "hdr.transcoded.mkv")
	spec := EncodeSpec{Codec: "h265", CRF: 30, Preset: "ultrafast", HDR: in, ChunkDuration: 3 * time.Second}
	if err := Run(ctx, src, dst, spec, nil); err != nil {
		t.Fatalf("chunked Run: %v", err)
	}
	out, err := ProbeHDR(ctx, dst)
	if err != nil || out == nil || !out.IsPQ() {
		t.Fatalf("ProbeHDR(dst) = %+v, %v", out, err)
	}
	if out.MaxCLL != 1000 || out.MaxFALL != 400 || out.Mastering == nil ||
		out.Mastering.x265MasterDisplay() != in.Mastering.x265MasterDisplay() {
		t.Errorf("static metadata lost: %+v / %+v", out, out.Mastering)
	}
}

// framesOf counts decoded video frames; chunking must never change the count.
func framesOf(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-count_frames", "-select_streams", "v:0",
		"-show_entries", "stream=nb_read_frames", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// TestChunkedMatchesSinglePassIntegration is the regression suite for real
// sources: open-GOP HEVC with B-frames, 29.97 fps, two audio tracks and a
// subtitle track. A chunked encode must keep exactly the frames, streams and
// duration of a single-pass encode, for relative paths, mkv/mp4 and h265/av1.
func TestChunkedMatchesSinglePassIntegration(t *testing.T) {
	dir := t.TempDir()
	srt := filepath.Join(dir, "s.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:01,000 --> 00:00:03,000\nhello\n\n2\n00:00:25,000 --> 00:00:28,000\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "real.mkv")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=s=320x180:d=40:r=30000/1001",
		"-f", "lavfi", "-i", "sine=d=40", "-f", "lavfi", "-i", "sine=f=880:d=40", "-i", srt,
		"-map", "0:v", "-map", "1:a", "-map", "2:a", "-map", "3",
		"-c:v", "libx265", "-preset", "ultrafast", "-x265-params",
		"open-gop=1:bframes=4:rc-lookahead=10:keyint=250:min-keyint=60:log-level=error",
		"-c:a", "aac", "-c:s", "srt", src)
	if err := gen.Run(); err != nil {
		t.Skipf("cannot generate test media: %v", err)
	}
	ctx := context.Background()

	cases := []struct {
		name, codec, ext string
		subs             string
	}{
		{"h265 mkv", "h265", "mkv", "copy"},
		{"av1 mkv", "av1", "mkv", "copy"},
		{"h265 mp4 drops subs", "h265", "mp4", "drop"}, // mp4 can't hold srt
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := EncodeSpec{Codec: c.codec, CRF: 32, Preset: "ultrafast", SubtitleMode: c.subs, Container: c.ext}
			if c.codec == "av1" {
				base.Preset = "12"
			}
			single := filepath.Join(dir, "single."+c.codec+"."+c.ext)
			if err := Run(ctx, src, single, base, nil); err != nil {
				t.Fatalf("single pass: %v", err)
			}
			chunked := base
			chunked.ChunkDuration = 10 * time.Second
			dst := filepath.Join(dir, "chunk."+c.codec+".transcoded."+c.ext)
			if err := Run(ctx, src, dst, chunked, nil); err != nil {
				t.Fatalf("chunked: %v", err)
			}
			// The source's own frame count is the truth: chunking must neither
			// drop nor duplicate frames. (Single-pass mp4 on some ffmpeg builds
			// pads one extra frame, so it is not a reliable oracle for mp4.)
			if a, b := framesOf(t, src), framesOf(t, dst); a != b {
				t.Errorf("frame count: source=%s chunked=%s", a, b)
			}
			dur := func(p string) time.Duration { d, _ := probeDuration(ctx, p); return d }
			if d1, d2 := dur(single), dur(dst); d2 < d1-100*time.Millisecond || d2 > d1+100*time.Millisecond {
				t.Errorf("duration: single=%v chunked=%v", d1, d2)
			}
			streams := func(p string) string {
				out, _ := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type", "-of", "csv=p=0", p).Output()
				return string(out)
			}
			if a, b := streams(single), streams(dst); a != b {
				t.Errorf("stream layout: single=%q chunked=%q", a, b)
			}
		})
	}

	// A relative source path must work: the concat list resolves entries
	// relative to its own directory.
	t.Run("relative path", func(t *testing.T) {
		wd, _ := os.Getwd()
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chdir(wd) }()
		spec := EncodeSpec{Codec: "h265", CRF: 32, Preset: "ultrafast", SubtitleMode: "drop", ChunkDuration: 10 * time.Second}
		if err := Run(ctx, "real.mkv", "rel.transcoded.mkv", spec, nil); err != nil {
			t.Fatalf("relative chunked run: %v", err)
		}
	})
}

// Video that starts after the audio (start_time 2 s): single pass keeps the
// offset and 825 frames; chunking must too, with no duplicated frames at the
// first boundary and the same video start.
func TestChunkedKeepsVideoStartOffsetIntegration(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "off.mkv")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-itsoffset", "2", "-f", "lavfi", "-i", "testsrc=s=160x120:d=33:r=25", "-f", "lavfi", "-i", "sine=d=35",
		"-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-g", "25", "-keyint_min", "25", "-sc_threshold", "0", "-c:a", "aac", src)
	if err := gen.Run(); err != nil {
		t.Skipf("cannot generate test media: %v", err)
	}
	ctx := context.Background()
	base := EncodeSpec{Codec: "h265", CRF: 32, Preset: "ultrafast", SubtitleMode: "copy"}
	single := filepath.Join(dir, "single.mkv")
	if err := Run(ctx, src, single, base, nil); err != nil {
		t.Fatal(err)
	}
	chunked := base
	chunked.ChunkDuration = 10 * time.Second
	dst := filepath.Join(dir, "off.transcoded.mkv")
	if err := Run(ctx, src, dst, chunked, nil); err != nil {
		t.Fatal(err)
	}
	if a, b := framesOf(t, src), framesOf(t, dst); a != b {
		t.Errorf("frame count: source=%s chunked=%s (duplicated frames at a boundary?)", a, b)
	}
	start := func(p string) float64 {
		out, _ := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=start_time", "-of", "csv=p=0", p).Output()
		v, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
		return v
	}
	// The 2 s offset must survive (a lost offset would read ~0). Encoder delay
	// shifts it by a fraction of a frame depending on the ffmpeg build.
	if a, b := start(single), start(dst); b < 1.9 || a-b > 0.05 || b-a > 0.05 {
		t.Errorf("video start_time: single=%v chunked=%v, want both ~2.0 within 50ms of each other", a, b)
	}
}

// Global metadata and chapters must survive the join.
func TestChunkedKeepsMetadataAndChaptersIntegration(t *testing.T) {
	dir := t.TempDir()
	chap := filepath.Join(dir, "chap.txt")
	if err := os.WriteFile(chap, []byte(";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=15000\ntitle=One\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=15000\nEND=30000\ntitle=Two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "m.mkv")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=s=160x120:d=30:r=25", "-f", "lavfi", "-i", "sine=d=30", "-i", chap,
		"-map", "0:v", "-map", "1:a", "-map_metadata", "2", "-map_chapters", "2", "-metadata", "title=MyTitle",
		"-c:v", "libx264", "-g", "25", "-keyint_min", "25", "-sc_threshold", "0", "-c:a", "aac", src)
	if err := gen.Run(); err != nil {
		t.Skipf("cannot generate test media: %v", err)
	}
	dst := filepath.Join(dir, "m.transcoded.mkv")
	spec := EncodeSpec{Codec: "h265", CRF: 32, Preset: "ultrafast", SubtitleMode: "copy", ChunkDuration: 10 * time.Second}
	if err := Run(context.Background(), src, dst, spec, nil); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format_tags=title:chapter=id", "-of", "default=noprint_wrappers=1", dst).Output()
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "TAG:title=MyTitle") {
		t.Errorf("global title lost:\n%s", s)
	}
	if n := strings.Count(s, "id="); n != 2 {
		t.Errorf("chapters = %d, want 2:\n%s", n, s)
	}
}

// Sources that share a base name in one directory (a.mkv, a.mp4) are encoded
// concurrently without sharing a work directory.
func TestChunkedConcurrentSameBasenameIntegration(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string) string {
		p := filepath.Join(dir, name)
		gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
			"-f", "lavfi", "-i", "testsrc=s=160x120:d=30:r=25", "-c:v", "libx264", "-g", "25", "-keyint_min", "25", "-sc_threshold", "0", p)
		if err := gen.Run(); err != nil {
			t.Skipf("cannot generate test media: %v", err)
		}
		return p
	}
	srcs := []string{mk("a.mkv"), mk("a.mp4")}
	spec := EncodeSpec{Codec: "h265", CRF: 32, Preset: "ultrafast", SubtitleMode: "drop", ChunkDuration: 10 * time.Second}
	dsts := []string{filepath.Join(dir, "a.transcoded.mkv"), filepath.Join(dir, "a.transcoded.mp4")}
	errs := make(chan error, 2)
	for i := range srcs {
		go func(i int) { errs <- Run(context.Background(), srcs[i], dsts[i], spec, nil) }(i)
	}
	for range srcs {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent chunked run: %v", err)
		}
	}
	for _, d := range dsts {
		if n := framesOf(t, d); n != "750" {
			t.Errorf("%s: %s frames, want 750", filepath.Base(d), n)
		}
	}
}

// videoPTS lists a file's video packet timestamps in seconds, ascending.
func videoPTS(t *testing.T, path string) []float64 {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "packet=pts_time", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	pts := parsePTSList(string(out))
	sort.Float64s(pts)
	return pts
}

// maxGap is the largest spacing between consecutive timestamps.
func maxGap(pts []float64) float64 {
	m := 0.0
	for i := 1; i < len(pts); i++ {
		if d := pts[i] - pts[i-1]; d > m {
			m = d
		}
	}
	return m
}

// The joined output must keep the source's frame timeline: every output frame
// lands where the same source frame was (within muxer rounding), with no
// whole-frame stalls at chunk seams. B-frame HEVC is the demanding case: the
// encoder delay shifts each chunk's first timestamp by several frames.
func TestChunkedHasNoTimingSeamsIntegration(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "seam.mkv")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-itsoffset", "1.5", "-f", "lavfi", "-i", "testsrc2=s=320x180:d=34:r=30000/1001",
		"-f", "lavfi", "-i", "sine=d=36", "-map", "0:v", "-map", "1:a",
		"-c:v", "libx265", "-preset", "ultrafast", "-x265-params", "bframes=4:rc-lookahead=10:log-level=error",
		"-c:a", "aac", src)
	if err := gen.Run(); err != nil {
		t.Skipf("cannot generate test media: %v", err)
	}
	for _, codec := range []string{"h265", "av1"} {
		t.Run(codec, func(t *testing.T) {
			preset := "ultrafast"
			if codec == "av1" {
				preset = "12"
			}
			dst := filepath.Join(dir, "seam."+codec+".transcoded.mkv")
			spec := EncodeSpec{Codec: codec, CRF: 32, Preset: preset, SubtitleMode: "drop", ChunkDuration: 10 * time.Second}
			if err := Run(context.Background(), src, dst, spec, nil); err != nil {
				t.Fatal(err)
			}
			in, out := videoPTS(t, src), videoPTS(t, dst)
			if len(in) != len(out) {
				t.Fatalf("frames: source=%d output=%d", len(in), len(out))
			}
			// Compare each frame's position relative to the stream's own first
			// frame: a stalled or shifted seam shows up as a step in this
			// series, whereas a constant offset (encoder delay differs between
			// ffmpeg builds) does not.
			worst := 0.0
			for i := range in {
				if d := math.Abs((out[i] - out[0]) - (in[i] - in[0])); d > worst {
					worst = d
				}
			}
			if worst > 0.01 {
				t.Errorf("worst per-frame timeline error = %.1f ms, want <= 10 ms (a stalled or shifted seam)", worst*1000)
			}
			// The video must still start where the source's did, give or take
			// one frame of encoder delay.
			if d := out[0] - in[0]; d < -0.045 || d > 0.045 {
				t.Errorf("video start moved by %.1f ms (source %.4f, output %.4f)", d*1000, in[0], out[0])
			}
			if g := maxGap(out); g > 0.045 { // nominal 0.0334; a stalled seam shows as ~0.066
				t.Errorf("largest frame gap = %.1f ms, want ~33 ms", g*1000)
			}
		})
	}
}
