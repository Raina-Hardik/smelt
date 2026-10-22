package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Segment and work-file names end in ".transcoded" before the extension so the
// worker's own-output filter never mistakes them for new sources.
const (
	segGlob   = "seg*.transcoded.mkv"
	segFormat = "seg%05d.transcoded.mkv"
	encFormat = "enc%05d.transcoded.mkv"
)

// ChunkDirSuffix is appended to the transient output path (minus extension) to
// name the per-file segment work directory, e.g. "movie.transcoded.chunks".
const ChunkDirSuffix = ".chunks"

func chunkDir(dst string) string {
	return strings.TrimSuffix(dst, filepath.Ext(dst)) + ChunkDirSuffix
}

// runChunked transcodes src in keyframe-aligned segments to bound how long any
// single ffmpeg process runs (useful on thermally limited hardware):
//
//  1. split the video stream with -c copy into ~ChunkDuration segments,
//  2. encode each segment video-only in its own process, pausing
//     ChunkCooldown between them,
//  3. concatenate the encoded segments and mux audio and subtitles straight
//     from the original source, so audio is processed exactly once and never
//     suffers per-segment seams.
//
// done=false with a nil error means the source could not be split into more
// than one segment and the caller should do a normal single-pass encode.
func runChunked(ctx context.Context, src, dst string, spec EncodeSpec, total time.Duration, onProgress func(ProgressEvent)) (done bool, err error) {
	dir := chunkDir(dst)
	if err := os.RemoveAll(dir); err != nil {
		return false, &OSError{FilePath: src, Err: err}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, &OSError{FilePath: src, Err: err}
	}
	defer func() { _ = os.RemoveAll(dir) }()

	if err := execFFmpeg(ctx, src, splitArgs(src, dir, spec.ChunkDuration), 0, nil); err != nil {
		return false, err
	}
	segs, err := filepath.Glob(filepath.Join(dir, segGlob))
	if err != nil {
		return false, &OSError{FilePath: src, Err: err}
	}
	sort.Strings(segs)
	if len(segs) < 2 {
		return false, nil
	}

	durs := make([]time.Duration, len(segs))
	for i, seg := range segs {
		if d, err := probeDuration(ctx, seg); err == nil && d > 0 {
			durs[i] = d
		} else {
			durs[i] = spec.ChunkDuration
		}
	}

	var offset time.Duration
	encoded := make([]string, len(segs))
	for i, seg := range segs {
		if i > 0 && spec.ChunkCooldown > 0 {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(spec.ChunkCooldown):
			}
		}
		encoded[i] = filepath.Join(dir, fmt.Sprintf(encFormat, i))
		err := execFFmpeg(ctx, src, segmentArgs(seg, encoded[i], spec), durs[i], chunkProgress(src, offset, total, onProgress))
		if err != nil {
			return false, err
		}
		offset += durs[i]
	}

	list := filepath.Join(dir, "list.txt")
	if err := os.WriteFile(list, []byte(concatList(encoded)), 0o644); err != nil {
		return false, &OSError{FilePath: src, Err: err}
	}
	if err := execFFmpeg(ctx, src, joinArgs(list, src, dst, spec), 0, nil); err != nil {
		return false, err
	}
	if onProgress != nil {
		onProgress(ProgressEvent{FilePath: src, Current: total, Total: total, Percent: 1})
	}
	return true, nil
}

// splitArgs cuts src's primary video stream into keyframe-aligned segments.
func splitArgs(src, dir string, chunk time.Duration) []string {
	return []string{
		"-hide_banner", "-loglevel", "error",
		"-i", src,
		"-map", "0:v:0", "-c", "copy", "-an", "-sn",
		"-f", "segment",
		"-segment_time", fmt.Sprintf("%.3f", chunk.Seconds()),
		"-reset_timestamps", "1",
		"-y", filepath.Join(dir, segFormat),
	}
}

// segmentArgs encodes one video-only segment with the run's video settings.
// Audio and subtitles are handled once, in the final join.
func segmentArgs(seg, dst string, spec EncodeSpec) []string {
	args := []string{"-hide_banner"}
	args = append(args, preInputArgs(spec)...)
	args = append(args, "-i", seg, "-map", "0:v:0", "-an", "-sn")
	args = append(args, videoFilterArgs(spec)...)
	args = append(args, rateControlArgs(spec)...)
	args = append(args, hdrArgs(spec)...)
	args = append(args, spec.ExtraArgs...)
	return append(args, "-y", dst)
}

// joinArgs concatenates the encoded segments (video copied) and takes audio
// and subtitles from the original source.
func joinArgs(list, src, dst string, spec EncodeSpec) []string {
	args := []string{
		"-hide_banner",
		"-f", "concat", "-safe", "0", "-i", list,
		"-i", src,
		"-map", "0:v:0", "-c:v", "copy",
		"-map", "1:a?",
	}
	args = append(args, audioArgs(spec)...)
	if strings.EqualFold(spec.SubtitleMode, "drop") {
		args = append(args, "-sn")
	} else {
		args = append(args, "-map", "1:s?", "-c:s", "copy")
	}
	args = append(args, containerArgs(spec)...)
	return append(args, "-y", dst)
}

func concatList(files []string) string {
	var b strings.Builder
	for _, f := range files {
		b.WriteString("file '")
		b.WriteString(strings.ReplaceAll(f, "'", `'\''`))
		b.WriteString("'\n")
	}
	return b.String()
}

// chunkProgress maps a segment's progress onto the whole file: offset is the
// duration already encoded in earlier segments.
func chunkProgress(src string, offset, total time.Duration, onProgress func(ProgressEvent)) func(ProgressEvent) {
	if onProgress == nil {
		return nil
	}
	return func(ev ProgressEvent) {
		cur := offset + ev.Current
		out := ProgressEvent{FilePath: src, Current: cur, Total: total, Speed: ev.Speed}
		if total > 0 {
			out.Percent = float64(cur) / float64(total)
			if out.Percent > 1 {
				out.Percent = 1
			}
			if ev.Speed > 0 && total > cur {
				out.ETA = time.Duration(float64(total-cur) / ev.Speed)
			}
		}
		onProgress(out)
	}
}
