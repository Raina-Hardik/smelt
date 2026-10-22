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
	want := []chunkRange{{0, 0, 10 * time.Second}, {10 * time.Second, 10 * time.Second, 10 * time.Second}, {20 * time.Second, 20 * time.Second, 0}}
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
	if !strings.Contains(got, "-ss 90.000 -t 30.000 -i in.mkv") {
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
	got := concatList([]string{"/a/enc0.mkv", "/b/it's.mkv"})
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
	if err := prepareChunkDir(foreign); err == nil {
		t.Error("must refuse a pre-existing directory without smelt's marker")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("foreign directory contents must be untouched: %v", err)
	}

	own := filepath.Join(root, "own.chunks")
	if err := prepareChunkDir(own); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(own, "enc00000.transcoded.mkv")
	_ = os.WriteFile(stale, []byte("x"), 0o644)
	if err := prepareChunkDir(own); err != nil {
		t.Fatalf("a leftover smelt directory must be reusable: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale chunk from an earlier attempt must be cleared")
	}
	if _, err := os.Stat(filepath.Join(own, ChunkMarker)); err != nil {
		t.Error("marker must be (re)written")
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
