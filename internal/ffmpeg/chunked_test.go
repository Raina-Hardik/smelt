package ffmpeg

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPlanChunks(t *testing.T) {
	if got := planChunks(15*time.Second, 10*time.Second, 0); got != nil {
		t.Errorf("too short to chunk: %v", got)
	}
	got := planChunks(35*time.Second, 10*time.Second, 0)
	want := []chunkRange{{seek: 0, offset: 0, length: 10 * time.Second}, {seek: 10 * time.Second, offset: 10 * time.Second, length: 10 * time.Second}, {seek: 20 * time.Second, offset: 20 * time.Second}}
	if !slices.Equal(got, want) {
		t.Errorf("planChunks(35s,10s) = %v, want %v (last absorbs remainder)", got, want)
	}
	// Ranges must partition the timeline: each start is the previous end.
	for i := 1; i < len(got); i++ {
		if got[i].seek != got[i-1].seek+got[i-1].length {
			t.Errorf("gap/overlap between chunk %d and %d: %v", i-1, i, got)
		}
	}
}

func TestSegmentArgsVideoOnly(t *testing.T) {
	spec := EncodeSpec{Codec: "h265", CRF: 22, Preset: "medium", AudioCodec: "aac", SubtitleMode: "copy", ExtraArgs: []string{"-vf", "scale=1280:-2"}}
	args := segmentArgs("in.mkv", "enc.mkv", chunkRange{seek: 90 * time.Second, offset: 90 * time.Second, length: 30 * time.Second}, spec)
	got := strings.Join(args, " ")
	if !strings.Contains(got, "-ss 90.000000 -t 30.000000 -i in.mkv") {
		t.Errorf("range must be input options before -i: %s", got)
	}
	for _, want := range []string{"-an", "-sn", "-c:v", "libx265", "-crf", "22", "-vf", "scale=1280:-2"} {
		if !slices.Contains(args, want) {
			t.Errorf("segmentArgs missing %q: %v", want, args)
		}
	}
	for _, bad := range []string{"-c:a", "-b:a", "0:a?", "-c:s"} {
		if slices.Contains(args, bad) {
			t.Errorf("segmentArgs must not touch audio/subs, found %q: %v", bad, args)
		}
	}
	if args[len(args)-1] != "enc.mkv" {
		t.Errorf("destination not last: %v", args)
	}

	last := strings.Join(segmentArgs("in.mkv", "enc.mkv", chunkRange{seek: 90 * time.Second, offset: 90 * time.Second}, spec), " ")
	if strings.Contains(last, "-t ") {
		t.Errorf("last chunk runs to EOF, must have no -t: %s", last)
	}
}

func TestJoinArgs(t *testing.T) {
	spec := EncodeSpec{Codec: "h265", AudioCodec: "opus", AudioBitrate: "128k", SubtitleMode: "copy", Container: "mp4"}
	got := strings.Join(joinArgs("l.txt", "in.mkv", "out.mp4", 0, spec), " ")
	for _, want := range []string{
		"-f concat -safe 0 -i l.txt -i in.mkv",
		"-map_metadata 1 -map_chapters 1 -map 0:v:0 -c:v copy -map 1:a?",
		"-c:a libopus -b:a 128k",
		"-map 1:s? -c:s copy",
		"-movflags +faststart -tag:v hvc1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("joinArgs missing %q in %s", want, got)
		}
	}

	drop := joinArgs("l.txt", "in.mkv", "out.mkv", 0, EncodeSpec{Codec: "h265", SubtitleMode: "drop"})
	if !slices.Contains(drop, "-sn") || slices.Contains(drop, "1:s?") {
		t.Errorf("subs=drop: %v", drop)
	}
}

func TestConcatListEscapesQuotes(t *testing.T) {
	got := concatList([]string{"/a/enc0.mkv", "/b/it's.mkv"}, nil)
	want := "file '/a/enc0.mkv'\nfile '/b/it'\\''s.mkv'\n"
	if got != want {
		t.Errorf("concatList = %q, want %q", got, want)
	}
}

func TestChunkDir(t *testing.T) {
	if got := chunkDir("/m/movie.transcoded.mkv"); got != "/m/movie.transcoded.mkv.chunks" {
		t.Errorf("chunkDir = %q", got)
	}
}

func TestChunkProgressMapsOntoWholeFile(t *testing.T) {
	var got ProgressEvent
	f := chunkProgress("in.mkv", 60*time.Second, 120*time.Second, func(ev ProgressEvent) { got = ev })
	f(ProgressEvent{Current: 15 * time.Second, Total: 60 * time.Second, Speed: 2})
	if got.Current != 75*time.Second || got.Total != 120*time.Second || got.FilePath != "in.mkv" {
		t.Errorf("event = %+v", got)
	}
	if got.Percent != 0.625 {
		t.Errorf("Percent = %v, want 0.625", got.Percent)
	}
	if got.ETA != 22500*time.Millisecond { // (120-75)s / 2x
		t.Errorf("ETA = %v", got.ETA)
	}
	if chunkProgress("x", 0, 0, nil) != nil {
		t.Error("nil callback must stay nil")
	}
}

func TestPlanChunksFollowsVideoTimeline(t *testing.T) {
	got := planChunks(35*time.Second, 10*time.Second, 2*time.Second)
	if got[0].seek != 2*time.Second || got[1].seek != 12*time.Second || got[2].seek != 22*time.Second {
		t.Errorf("seeks must be vstart + offset: %v", got)
	}
	if got[1].offset != 10*time.Second {
		t.Errorf("progress offset must ignore vstart: %v", got[1])
	}
}

func TestJoinArgsRestoresVideoStartOffset(t *testing.T) {
	spec := EncodeSpec{Codec: "h265", SubtitleMode: "copy"}
	got := strings.Join(joinArgs("l.txt", "in.mkv", "out.mkv", 2*time.Second, spec), " ")
	if !strings.Contains(got, "-itsoffset 2.000 -f concat") {
		t.Errorf("offset must precede the concat input: %s", got)
	}
	if strings.Contains(strings.Join(joinArgs("l.txt", "in.mkv", "out.mkv", 0, spec), " "), "-itsoffset") {
		t.Error("no offset flag when the video starts at 0")
	}
}

func TestParseVideoStartOffset(t *testing.T) {
	cases := map[string]time.Duration{
		`{"streams":[{"start_time":"2.000000"}],"format":{"start_time":"0.000000"}}`: 2 * time.Second,
		`{"streams":[{"start_time":"6.900000"}],"format":{"start_time":"6.877000"}}`: 23 * time.Millisecond,
		`{"streams":[{"start_time":"0.000000"}],"format":{"start_time":"0.000000"}}`: 0,
		`{"streams":[{"start_time":"N/A"}],"format":{"start_time":"0.000000"}}`:      0,
		`{"streams":[{"start_time":"1.0"}],"format":{"start_time":"N/A"}}`:           time.Second,
		`{"streams":[{"start_time":"0.5"}],"format":{"start_time":"2.0"}}`:           0, // never negative
		`{"streams":[],"format":{}}`: 0,
		`not json`:                   0,
	}
	for in, want := range cases {
		got := parseVideoStartOffset([]byte(in))
		if d := got - want; d < -time.Millisecond || d > time.Millisecond {
			t.Errorf("%s -> %v, want %v", in, got, want)
		}
	}
}

func TestChunkUnsafeArgs(t *testing.T) {
	for _, safe := range [][]string{
		nil,
		{"-vf", "scale=1280:-2"},
		{"-vf", "scale=1280:-2,format=yuv420p"},
		{"-metadata", "title=x"},
		{"-x265-params", "aq-mode=3"},
	} {
		if got := ChunkUnsafeArgs(safe); len(got) != 0 {
			t.Errorf("%v flagged as unsafe: %v", safe, got)
		}
	}
	for _, unsafe := range [][]string{
		{"-ss", "5"}, {"-t", "20"}, {"-to", "20"}, {"-r", "12"}, {"-vf", "fps=12"},
		{"-vf", "scale=640:-2,trim=duration=5"}, {"-filter:v", "setpts=0.5*PTS"},
		{"-map", "0:v:1"}, {"-filter_complex", "[0:v]fps=10[v]"}, {"-vframes", "100"}, {"-itsoffset", "1"},
	} {
		if got := ChunkUnsafeArgs(unsafe); len(got) == 0 {
			t.Errorf("%v should be flagged unsafe for chunking", unsafe)
		}
	}
}

func TestPrepareChunkDirRefusesForeignDirectories(t *testing.T) {
	root := t.TempDir()
	foreign := filepath.Join(root, "mine.chunks")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(foreign, "keep.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareChunkDir(foreign, "k"); err == nil {
		t.Error("must refuse a pre-existing directory without smelt's marker")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("foreign directory contents must be untouched: %v", err)
	}
}

func TestPrepareChunkDirResumesOnlyMatchingKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "own.chunks")
	if resumed, err := prepareChunkDir(dir, "key-A"); err != nil || resumed {
		t.Fatalf("fresh dir: resumed=%v err=%v", resumed, err)
	}
	done := filepath.Join(dir, "enc00000.transcoded.mkv")
	part := filepath.Join(dir, "enc00001.part.transcoded.mkv")
	list := filepath.Join(dir, "list.txt")
	for _, f := range []string{done, part, list} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	resumed, err := prepareChunkDir(dir, "key-A")
	if err != nil || !resumed {
		t.Fatalf("same key must resume: resumed=%v err=%v", resumed, err)
	}
	if _, err := os.Stat(done); err != nil {
		t.Error("finished chunk must survive a resume")
	}
	for _, gone := range []string{part, list} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s (mid-write leftovers) must be dropped on resume", filepath.Base(gone))
		}
	}

	resumed, err = prepareChunkDir(dir, "key-B")
	if err != nil || resumed {
		t.Fatalf("different key must start over: resumed=%v err=%v", resumed, err)
	}
	if _, err := os.Stat(done); !os.IsNotExist(err) {
		t.Error("stale chunk from different settings must be cleared")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, ChunkMarker)); string(got) != "key-B" {
		t.Errorf("marker = %q, want the new key", got)
	}
}

func TestChunkKeyTracksEverythingThatChangesTheOutput(t *testing.T) {
	src := filepath.Join(t.TempDir(), "in.mkv")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := []chunkRange{{seek: 0, offset: 0, length: 10 * time.Second, expect: 300}, {seek: 10 * time.Second, offset: 10 * time.Second, expect: 299}}
	base := EncodeSpec{Codec: "h265", CRF: 24, Preset: "medium", ChunkDuration: 10 * time.Second}
	k0 := chunkKey(src, base, plan)
	if k0 != chunkKey(src, base, plan) {
		t.Fatal("key must be deterministic")
	}
	same := base
	same.AudioCodec, same.AudioBitrate, same.SubtitleMode, same.ChunkCooldown = "opus", "128k", "drop", time.Minute
	same.DecodeBackend = "qsv"
	same.ExtraArgs = []string{"-metadata", "title=x", "-b:a", "96k"}
	if chunkKey(src, same, plan) != k0 {
		t.Error("audio/subtitle/cooldown/decode-backend/output-arg changes do not alter chunk content and must not invalidate chunks")
	}

	differs := map[string]func(*EncodeSpec){
		"crf":       func(s *EncodeSpec) { s.CRF = 30 },
		"preset":    func(s *EncodeSpec) { s.Preset = "slow" },
		"codec":     func(s *EncodeSpec) { s.Codec = "av1" },
		"encoder":   func(s *EncodeSpec) { s.Encoder = "hevc_qsv"; s.Backend = "qsv" },
		"chunk len": func(s *EncodeSpec) { s.ChunkDuration = 20 * time.Second },
		"video arg": func(s *EncodeSpec) { s.ExtraArgs = []string{"-vf", "scale=1280:-2"} },
		"hdr":       func(s *EncodeSpec) { s.HDR = &HDRInfo{ColorTransfer: "smpte2084", MaxCLL: 1000} },
	}
	for name, mut := range differs {
		sp := base
		mut(&sp)
		if chunkKey(src, sp, plan) == k0 {
			t.Errorf("changing %s must invalidate resumable chunks", name)
		}
	}
	if chunkKey(src, base, plan[:1]) == k0 {
		t.Error("a different plan must invalidate resumable chunks")
	}
	if err := os.WriteFile(src, []byte("changed content!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if chunkKey(src, base, plan) == k0 {
		t.Error("a modified source file must invalidate resumable chunks")
	}
}

func TestSplitExtraArgs(t *testing.T) {
	video, output := splitExtraArgs([]string{
		"-vf", "scale=1280:-2", "-x265-params", "aq-mode=3", "-profile:v", "main10",
		"-metadata", "title=X", "-movflags", "+faststart", "-b:a", "96k", "-c:a", "libopus",
		"-af", "volume=2", "-metadata:s:a:0", "language=eng", "-disposition:a:0", "default",
		"-tag:v", "hvc1", "-pix_fmt", "yuv420p10le", "-filter:a", "loudnorm",
	})
	wantVideo := []string{"-vf", "scale=1280:-2", "-x265-params", "aq-mode=3", "-profile:v", "main10", "-pix_fmt", "yuv420p10le"}
	if !slices.Equal(video, wantVideo) {
		t.Errorf("video = %v, want %v", video, wantVideo)
	}
	wantOutput := []string{
		"-metadata", "title=X", "-movflags", "+faststart", "-b:a", "96k", "-c:a", "libopus",
		"-af", "volume=2", "-metadata:s:a:0", "language=eng", "-disposition:a:0", "default",
		"-tag:v", "hvc1", "-filter:a", "loudnorm",
	}
	if !slices.Equal(output, wantOutput) {
		t.Errorf("output = %v, want %v", output, wantOutput)
	}
	if v, o := splitExtraArgs(nil); v != nil || o != nil {
		t.Errorf("nil in, nil out: %v %v", v, o)
	}
	// A trailing output flag with no value must not panic.
	if _, o := splitExtraArgs([]string{"-metadata"}); len(o) != 1 {
		t.Errorf("dangling flag: %v", o)
	}
}

func TestChunkedAppliesExtraArgsToTheRightStage(t *testing.T) {
	spec := EncodeSpec{Codec: "h265", CRF: 24, SubtitleMode: "copy", ExtraArgs: []string{"-vf", "scale=1280:-2", "-metadata", "title=X", "-movflags", "+faststart"}}
	seg := strings.Join(segmentArgs("in.mkv", "enc.mkv", chunkRange{length: time.Second}, spec), " ")
	if !strings.Contains(seg, "-vf scale=1280:-2") || strings.Contains(seg, "-metadata") || strings.Contains(seg, "-movflags") {
		t.Errorf("segment args must carry only video arguments: %s", seg)
	}
	join := strings.Join(joinArgs("l.txt", "in.mkv", "out.mkv", 0, spec), " ")
	if !strings.Contains(join, "-metadata title=X -movflags +faststart") || strings.Contains(join, "scale=") {
		t.Errorf("join args must carry only container/metadata arguments: %s", join)
	}
	if !strings.HasSuffix(join, "-y out.mkv") {
		t.Errorf("destination must stay last: %s", join)
	}
}

func TestJoinErrorIsNotExecError(t *testing.T) {
	var err error = &JoinError{&ExecError{FilePath: "x", ExitCode: 1, Stderr: "boom"}}
	if _, ok := err.(*ExecError); ok {
		t.Error("JoinError must not satisfy a *ExecError type assertion (it would trigger the hw-decode retry)")
	}
	var ee *ExecError
	if !errors.As(err, &ee) || ee.ExitCode != 1 {
		t.Error("JoinError must still unwrap to the ExecError")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("message lost: %v", err)
	}
}

// framesAt returns n timestamps at the given fps starting at start.
func framesAt(n int, fps, start float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = start + float64(i)/fps
	}
	return out
}

// Every planned boundary must sit between two frames - never on one - and the
// per-chunk expected counts must account for every frame exactly once.
func TestPlanChunksFromPTSBoundariesAreMidGapAndPartition(t *testing.T) {
	cases := map[string][]float64{
		"29.97fps":     framesAt(1199, 30000.0/1001, 0),
		"24fps":        framesAt(960, 24, 0),
		"25fps+offset": framesAt(825, 25, 2.0),
	}
	for name, pts := range cases {
		vstart := time.Duration(pts[0] * float64(time.Second))
		chunks := planChunksFromPTS(pts, 10*time.Second, vstart)
		if len(chunks) < 3 {
			t.Fatalf("%s: got %d chunks", name, len(chunks))
		}
		sum := 0
		for _, c := range chunks {
			sum += c.expect
			if c.expect <= 0 {
				t.Errorf("%s: chunk with expect=%d", name, c.expect)
			}
		}
		if sum != len(pts) {
			t.Errorf("%s: expected counts sum to %d, source has %d frames", name, sum, len(pts))
		}
		// Absolute boundary times (file timeline) vs. the frame timestamps.
		var minGap = 1e9
		for k := 1; k < len(chunks); k++ {
			b := chunks[k].seek.Seconds() // vstart==pts[0], file start 0 => absolute
			for _, p := range pts {
				if d := abs(p - b); d < minGap {
					minGap = d
				}
			}
		}
		frame := 1 / (30000.0 / 1001)
		switch name {
		case "24fps":
			frame = 1.0 / 24
		case "25fps+offset":
			frame = 1.0 / 25
		}
		if minGap < frame/2-0.0005 {
			t.Errorf("%s: a boundary is only %.4fs from a frame (< half a frame, %.4fs): decoders may disagree on which chunk owns it", name, minGap, frame/2)
		}
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func TestPlanChunksFromPTSVariableFrameRate(t *testing.T) {
	// 20 s at 30 fps, then 20 s at 10 fps.
	pts := framesAt(600, 30, 0)
	pts = append(pts, framesAt(200, 10, 20.0)...)
	chunks := planChunksFromPTS(pts, 10*time.Second, 0)
	sum := 0
	for _, c := range chunks {
		sum += c.expect
	}
	if sum != len(pts) || len(chunks) != 4 {
		t.Fatalf("chunks=%d sum=%d, want 4 chunks covering %d frames", len(chunks), sum, len(pts))
	}
	if chunks[1].expect != 300 || chunks[2].expect != 100 {
		t.Errorf("per-chunk counts follow the real timestamps: %+v", chunks)
	}
}

func TestPlanChunksFromPTSEdgeCases(t *testing.T) {
	if planChunksFromPTS(nil, 10*time.Second, 0) != nil {
		t.Error("no timestamps: nil")
	}
	if planChunksFromPTS(framesAt(100, 25, 0), 10*time.Second, 0) != nil {
		t.Error("4 s source with 10 s chunks: nil")
	}
	// B-frame reorder: timestamps arrive out of order.
	pts := framesAt(600, 24, 0)
	pts[10], pts[11] = pts[11], pts[10]
	pts[300], pts[302] = pts[302], pts[300]
	if c := planChunksFromPTS(pts, 10*time.Second, 0); len(c) < 2 {
		t.Errorf("reordered timestamps must still plan: %v", c)
	}
}

func TestParsePTSList(t *testing.T) {
	got := parsePTSList("0.000000,\n0.041000,\nN/A,\n\n0.083000\n")
	if len(got) != 3 || got[2] != 0.083 {
		t.Errorf("parsePTSList = %v", got)
	}
}

func TestConcatListCarriesExplicitDurations(t *testing.T) {
	got := concatList([]string{"a.mkv", "b.mkv"}, []time.Duration{10010 * time.Millisecond, 13981 * time.Millisecond})
	want := "file 'a.mkv'\nduration 10.010000\nfile 'b.mkv'\nduration 13.981000\n"
	if got != want {
		t.Errorf("concatList = %q, want %q", got, want)
	}
	// If any span is unknown, fall back to the demuxer's own measurement
	// rather than emitting a wrong duration.
	partial := concatList([]string{"a.mkv", "b.mkv"}, []time.Duration{10 * time.Second, 0})
	if strings.Contains(partial, "duration") {
		t.Errorf("partial spans must not be emitted: %q", partial)
	}
}

func TestPlanChunksFromPTSSpansTileTheTimeline(t *testing.T) {
	pts := framesAt(1019, 30000.0/1001, 1.5)
	chunks := planChunksFromPTS(pts, 10*time.Second, 1500*time.Millisecond)
	var total time.Duration
	for _, c := range chunks {
		if c.span <= 0 {
			t.Fatalf("every chunk needs a span: %+v", c)
		}
		total += c.span
	}
	end := time.Duration((pts[len(pts)-1] - pts[0] + 1/(30000.0/1001)) * float64(time.Second))
	if d := total - end; d < -2*time.Millisecond || d > 2*time.Millisecond {
		t.Errorf("spans sum to %v, want the full video span %v", total, end)
	}
	if d := chunks[1].span - chunks[0].span; d < -40*time.Millisecond || d > 40*time.Millisecond {
		t.Errorf("neighbouring spans should be ~10 s: %v vs %v", chunks[0].span, chunks[1].span)
	}
}
