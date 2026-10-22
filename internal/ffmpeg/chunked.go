package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Encoded-segment names end in ".transcoded" before the extension so the
// worker's own-output filter never mistakes them for new sources.
const encFormat = "enc%05d.transcoded.mkv"

// ChunkDirSuffix is appended to the transient output path (minus extension) to
// name the per-file segment work directory, e.g. "movie.transcoded.chunks".
const ChunkDirSuffix = ".chunks"

func chunkDir(dst string) string {
	return strings.TrimSuffix(dst, filepath.Ext(dst)) + ChunkDirSuffix
}

// chunkRange is one time range of the source. length 0 means "to end of file".
type chunkRange struct {
	start, length time.Duration
}

// planChunks divides total into ranges of chunk, the last absorbing the
// remainder so no tiny trailing chunk is produced. It returns nil when total
// is too short for at least two chunks.
func planChunks(total, chunk time.Duration) []chunkRange {
	n := int(total / chunk)
	if n < 2 {
		return nil
	}
	out := make([]chunkRange, n)
	for i := range out {
		out[i] = chunkRange{start: time.Duration(i) * chunk, length: chunk}
	}
	out[n-1].length = 0
	return out
}

// runChunked transcodes src in time-range chunks to bound how long any single
// ffmpeg process runs (useful on thermally limited hardware):
//
//  1. each ~ChunkDuration range is encoded video-only by its own process
//     reading the original with -ss/-t, pausing ChunkCooldown between them,
//  2. the encoded chunks are concatenated, with audio and subtitles taken
//     straight from the original, so audio is processed exactly once and
//     never suffers per-chunk seams.
//
// Chunks are cut by decoding, not by stream copy: copying at keyframes drops
// the leading B-frames of every open-GOP segment (common in HEVC), silently
// losing frames at each boundary. The ranges partition the timeline, so every
// source frame lands in exactly one chunk.
//
// done=false with a nil error means the source is too short to chunk and the
// caller should do a normal single-pass encode.
func runChunked(ctx context.Context, src, dst string, spec EncodeSpec, total time.Duration, onProgress func(ProgressEvent)) (done bool, err error) {
	chunks := planChunks(total, spec.ChunkDuration)
	if chunks == nil {
		return false, nil
	}
	dir := chunkDir(dst)
	if err := os.RemoveAll(dir); err != nil {
		return false, &OSError{FilePath: src, Err: err}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, &OSError{FilePath: src, Err: err}
	}
	defer func() { _ = os.RemoveAll(dir) }()

	names := make([]string, len(chunks))
	for i, c := range chunks {
		if i > 0 && spec.ChunkCooldown > 0 {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(spec.ChunkCooldown):
			}
		}
		names[i] = fmt.Sprintf(encFormat, i)
		length := c.length
		if length == 0 {
			length = total - c.start
		}
		args := segmentArgs(src, filepath.Join(dir, names[i]), c, spec)
		if err := execFFmpeg(ctx, src, args, length, chunkProgress(src, c.start, total, onProgress)); err != nil {
			return false, err
		}
	}

	list := filepath.Join(dir, "list.txt")
	// The concat demuxer resolves entries relative to the list file's own
	// directory, so list bare file names: a relative --src would otherwise
	// double the path.
	if err := os.WriteFile(list, []byte(concatList(names)), 0o644); err != nil {
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

// segmentArgs encodes one video-only time range of src with the run's video
// settings. -ss/-t are input options: ffmpeg seeks to the previous keyframe
// and discards up to the exact start, so cuts are frame-accurate. Audio and
// subtitles are handled once, in the final join.
func segmentArgs(src, dst string, c chunkRange, spec EncodeSpec) []string {
	args := []string{"-hide_banner"}
	args = append(args, preInputArgs(spec)...)
	args = append(args, "-ss", fmt.Sprintf("%.3f", c.start.Seconds()))
	if c.length > 0 {
		args = append(args, "-t", fmt.Sprintf("%.3f", c.length.Seconds()))
	}
	args = append(args, "-i", src, "-map", "0:v:0", "-an", "-sn")
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
