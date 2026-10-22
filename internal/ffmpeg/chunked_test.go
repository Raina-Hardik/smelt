package ffmpeg

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPlanChunks(t *testing.T) {
	if got := planChunks(15*time.Second, 10*time.Second); got != nil {
		t.Errorf("too short to chunk: %v", got)
	}
	got := planChunks(35*time.Second, 10*time.Second)
	want := []chunkRange{{0, 10 * time.Second}, {10 * time.Second, 10 * time.Second}, {20 * time.Second, 0}}
	if !slices.Equal(got, want) {
		t.Errorf("planChunks(35s,10s) = %v, want %v (last absorbs remainder)", got, want)
	}
	// Ranges must partition the timeline: each start is the previous end.
	for i := 1; i < len(got); i++ {
		if got[i].start != got[i-1].start+got[i-1].length {
			t.Errorf("gap/overlap between chunk %d and %d: %v", i-1, i, got)
		}
	}
}

func TestSegmentArgsVideoOnly(t *testing.T) {
	spec := EncodeSpec{Codec: "h265", CRF: 22, Preset: "medium", AudioCodec: "aac", SubtitleMode: "copy", ExtraArgs: []string{"-vf", "scale=1280:-2"}}
	args := segmentArgs("in.mkv", "enc.mkv", chunkRange{start: 90 * time.Second, length: 30 * time.Second}, spec)
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

	last := strings.Join(segmentArgs("in.mkv", "enc.mkv", chunkRange{start: 90 * time.Second}, spec), " ")
	if strings.Contains(last, "-t ") {
		t.Errorf("last chunk runs to EOF, must have no -t: %s", last)
	}
}

func TestJoinArgs(t *testing.T) {
	spec := EncodeSpec{Codec: "h265", AudioCodec: "opus", AudioBitrate: "128k", SubtitleMode: "copy", Container: "mp4"}
	got := strings.Join(joinArgs("l.txt", "in.mkv", "out.mp4", spec), " ")
	for _, want := range []string{
		"-f concat -safe 0 -i l.txt -i in.mkv",
		"-map 0:v:0 -c:v copy -map 1:a?",
		"-c:a libopus -b:a 128k",
		"-map 1:s? -c:s copy",
		"-movflags +faststart -tag:v hvc1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("joinArgs missing %q in %s", want, got)
		}
	}

	drop := joinArgs("l.txt", "in.mkv", "out.mkv", EncodeSpec{Codec: "h265", SubtitleMode: "drop"})
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
	if got := chunkDir("/m/movie.transcoded.mkv"); got != "/m/movie.transcoded.chunks" {
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
