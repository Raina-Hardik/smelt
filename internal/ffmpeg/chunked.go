package ffmpeg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Encoded-segment names end in ".transcoded" before the extension so the
// worker's own-output filter never mistakes them for new sources.
const encFormat = "enc%05d.transcoded.mkv"

// ChunkDirSuffix is appended to the transient output path to name the
// per-file work directory, e.g. "movie.transcoded.mkv.chunks". Using the whole
// transient name (extension included) keeps two sources that share a base name
// ("a.mkv", "a.mp4") from sharing a directory.
const ChunkDirSuffix = ".chunks"

func chunkDir(dst string) string { return dst + ChunkDirSuffix }

// chunkRange is one time range of the source. seek is the file-relative
// position handed to -ss; offset is how far into the video timeline the range
// begins (progress accounting); length 0 means "to end of file".
type chunkRange struct {
	seek, offset, length time.Duration
}

// planChunks divides total into ranges of chunk, the last absorbing the
// remainder so no tiny trailing chunk is produced. vstart is where the video
// stream begins relative to the container start: ranges are laid along the
// video's own timeline (seek = vstart + offset), because -t counts from the
// first packet — a video that starts 2 s in would otherwise have every range
// overlap the next by 2 s. It returns nil when total is too short for at least
// two chunks.
func planChunks(total, chunk, vstart time.Duration) []chunkRange {
	n := int(total / chunk)
	if n < 2 {
		return nil
	}
	out := make([]chunkRange, n)
	for i := range out {
		off := time.Duration(i) * chunk
		out[i] = chunkRange{seek: vstart + off, offset: off, length: chunk}
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
	vstart := probeVideoStartOffset(ctx, src)
	chunks := planChunks(total, spec.ChunkDuration, vstart)
	if chunks == nil {
		return false, nil
	}
	dir := chunkDir(dst)
	if err := prepareChunkDir(dir); err != nil {
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
			length = total - c.offset
		}
		args := segmentArgs(src, filepath.Join(dir, names[i]), c, spec)
		if err := execFFmpeg(ctx, src, args, length, chunkProgress(src, c.offset, total, onProgress)); err != nil {
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
	if err := execFFmpeg(ctx, src, joinArgs(list, src, dst, vstart, spec), 0, nil); err != nil {
		// A join failure is unrelated to decoding; keep it distinct so the
		// worker's hw-decode retry does not redo every chunk.
		var ee *ExecError
		if errors.As(err, &ee) {
			return false, &JoinError{ee}
		}
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
	args = append(args, "-ss", fmt.Sprintf("%.3f", c.seek.Seconds()))
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
func joinArgs(list, src, dst string, vstart time.Duration, spec EncodeSpec) []string {
	args := []string{"-hide_banner"}
	// Each chunk was rebased to start at 0; restore the video's original
	// offset against the audio, which is read from the source untouched.
	if vstart > time.Millisecond {
		args = append(args, "-itsoffset", fmt.Sprintf("%.3f", vstart.Seconds()))
	}
	args = append(args,
		"-f", "concat", "-safe", "0", "-i", list,
		"-i", src,
		"-map_metadata", "1", "-map_chapters", "1",
		"-map", "0:v:0", "-c:v", "copy",
		"-map", "1:a?",
	)
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

// JoinError is a failure of the final concat/remux step. It deliberately is
// not an *ExecError, so callers that retry on ExecError (hardware-decode
// fallback) do not re-run every chunk for a problem that is not about decode.
type JoinError struct{ *ExecError }

func (e *JoinError) Unwrap() error { return e.ExecError }

// chunkMarker identifies a work directory as smelt's own. It guards both
// reuse (a pre-existing directory without it is never wiped) and `smelt clean`.
const ChunkMarker = ".smelt-chunks"

// prepareChunkDir creates a fresh work directory, clearing leftovers from an
// earlier attempt only if they carry smelt's marker.
func prepareChunkDir(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		if _, merr := os.Stat(filepath.Join(dir, ChunkMarker)); merr != nil {
			return fmt.Errorf("%s exists and is not a smelt work directory; refusing to reuse it", dir)
		}
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ChunkMarker), nil, 0o644)
}

// probeVideoStartOffset returns where the primary video stream starts relative
// to the container start (0 when unknown or negative).
func probeVideoStartOffset(ctx context.Context, path string) time.Duration {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=start_time:format=start_time",
		"-of", "json", path).Output()
	if err != nil {
		return 0
	}
	return parseVideoStartOffset(out)
}

func parseVideoStartOffset(data []byte) time.Duration {
	var doc struct {
		Streams []struct {
			StartTime string `json:"start_time"`
		} `json:"streams"`
		Format struct {
			StartTime string `json:"start_time"`
		} `json:"format"`
	}
	if json.Unmarshal(data, &doc) != nil || len(doc.Streams) == 0 {
		return 0
	}
	vs, err := strconv.ParseFloat(doc.Streams[0].StartTime, 64)
	if err != nil {
		return 0
	}
	fs, _ := strconv.ParseFloat(doc.Format.StartTime, 64) // N/A -> 0
	if d := vs - fs; d > 0 {
		return time.Duration(d * float64(time.Second))
	}
	return 0
}

// ChunkUnsafeArgs returns the ff args in extra that alter the timeline or
// stream selection in ways that cannot be applied independently to each chunk
// (a trim, an fps change, a custom map, ...). Chunked mode must not be used
// with them: each chunk would apply them again and the result would differ
// from a single-pass encode.
func ChunkUnsafeArgs(extra []string) []string {
	unsafe := map[string]bool{
		"-ss": true, "-t": true, "-to": true, "-itsoffset": true, "-r": true,
		"-fps_mode": true, "-vsync": true, "-vframes": true, "-frames": true,
		"-frames:v": true, "-map": true, "-filter_complex": true, "-lavfi": true,
		"-shortest": true, "-copyts": true,
	}
	timeFilter := func(v string) bool {
		for _, f := range []string{"fps", "trim", "setpts", "select", "framerate", "minterpolate", "tpad", "loop"} {
			if strings.Contains(v, f) {
				return true
			}
		}
		return false
	}
	var found []string
	for i, a := range extra {
		if unsafe[a] {
			found = append(found, a)
		}
		if (a == "-vf" || a == "-filter:v") && i+1 < len(extra) && timeFilter(extra[i+1]) {
			found = append(found, a+" "+extra[i+1])
		}
	}
	return found
}
