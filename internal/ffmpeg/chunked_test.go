package ffmpeg

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSplitArgs(t *testing.T) {
	got := strings.Join(splitArgs("/m/in.mkv", "/m/w", 90*time.Second), " ")
	for _, want := range []string{"-i /m/in.mkv", "-map 0:v:0", "-c copy", "-an -sn", "-f segment", "-segment_time 90.000", "-reset_timestamps 1", "/m/w/seg%05d.transcoded.mkv"} {
		if !strings.Contains(got, want) {
			t.Errorf("splitArgs missing %q in %s", want, got)
		}
	}
}

func TestSegmentArgsVideoOnly(t *testing.T) {
	spec := EncodeSpec{Codec: "h265", CRF: 22, Preset: "medium", AudioCodec: "aac", SubtitleMode: "copy", ExtraArgs: []string{"-vf", "scale=1280:-2"}}
	args := segmentArgs("seg.mkv", "enc.mkv", spec)
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
