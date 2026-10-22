package ffmpeg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// Encoded-segment names end in ".transcoded" before the extension so the
// worker's own-output filter never mistakes them for new sources.
const (
	encFormat  = "enc%05d.transcoded.mkv"
	partFormat = "enc%05d.part.transcoded.mkv" // in-flight encode, renamed once verified
)

// ChunkDirSuffix is appended to the transient output path to name the
// per-file work directory, e.g. "movie.transcoded.mkv.chunks". Using the whole
// transient name (extension included) keeps two sources that share a base name
// ("a.mkv", "a.mp4") from sharing a directory.
const ChunkDirSuffix = ".chunks"

func chunkDir(dst string) string { return dst + ChunkDirSuffix }

// chunkRange is one time range of the source. seek is the file-relative
// position handed to -ss; offset is how far into the video timeline the range
// begins (progress accounting); length 0 means "to end of file". expect is the
// exact number of source frames the range must yield (0 = unknown, unchecked).
// span is the source time from this range's first frame to the next range's
// first frame (the last range runs to the end of its final frame); it is fed
// to the concat demuxer so seams land exactly where the source's frames were.
type chunkRange struct {
	seek, offset, length, span time.Duration
	expect                     int
}

// planChunks divides total into ranges of fixed length when frame timestamps are
// unavailable. It divides total into ranges of chunk, the last absorbing the
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

// planChunksFromPTS lays chunk boundaries on the source's real frame
// timestamps. pts are the video frames' presentation times (any order, same
// timeline); vstart is where the video begins relative to the container
// start. Each boundary is placed midway between the two frames that straddle
// the nominal cut, so no frame sits on a boundary: decoders disagree about
// which side such a frame belongs to (hardware decoders in particular), and
// midpoints also make variable-frame-rate sources exact. Every range knows the
// exact frame count it must produce, which runChunked verifies. It returns nil
// when the source is too short for at least two chunks.
func planChunksFromPTS(pts []float64, chunk, vstart time.Duration) []chunkRange {
	if len(pts) < 2 || chunk <= 0 {
		return nil
	}
	rel := make([]float64, len(pts))
	sorted := append([]float64(nil), pts...)
	sort.Float64s(sorted)
	for i, v := range sorted {
		rel[i] = v - sorted[0]
	}
	last := len(rel) - 1
	span := rel[last] + (rel[last] - rel[last-1]) // end of the last frame
	n := int(span / chunk.Seconds())
	if n < 2 {
		return nil
	}

	// cut[k] is the index of the first frame of chunk k.
	cut := []int{0}
	for k := 1; k < n; k++ {
		idx := sort.SearchFloat64s(rel, float64(k)*chunk.Seconds())
		if idx <= cut[len(cut)-1] || idx >= len(rel) {
			continue // degenerate (sparse timestamps): skip this boundary
		}
		cut = append(cut, idx)
	}
	if len(cut) < 2 {
		return nil
	}

	// Boundary times: midway between frames cut-1 and cut. The first chunk
	// starts half a gap before the first frame so that frame is not on the edge.
	half := (rel[1] - rel[0]) / 2
	bound := make([]float64, len(cut))
	bound[0] = -half
	for k := 1; k < len(cut); k++ {
		bound[k] = (rel[cut[k]-1] + rel[cut[k]]) / 2
	}

	dur := func(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }
	out := make([]chunkRange, len(cut))
	for k := range cut {
		startRel := dur(bound[k]) // video-relative start of this chunk
		if vstart+startRel < 0 {
			startRel = -vstart // video begins at the container start: cannot seek earlier
		}
		r := chunkRange{seek: vstart + startRel, offset: dur(rel[cut[k]])}
		end := len(rel)
		if k+1 < len(cut) {
			end = cut[k+1]
			r.length = dur(bound[k+1]) - startRel
			r.span = dur(rel[cut[k+1]] - rel[cut[k]])
		} else {
			r.span = dur(span - rel[cut[k]])
		}
		r.expect = end - cut[k]
		out[k] = r
	}
	return out
}

// probeVideoPTS lists the primary video stream's packet timestamps (seconds).
// It only demuxes — no decode — but does read the whole file once.
func probeVideoPTS(ctx context.Context, path string) ([]float64, error) {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error",
		"-select_streams", "v:0", "-show_entries", "packet=pts_time",
		"-of", "csv=p=0", path).Output()
	if err != nil {
		return nil, err
	}
	return parsePTSList(string(out)), nil
}

func parsePTSList(out string) []float64 {
	var pts []float64
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, ","))
		if line == "" || line == "N/A" {
			continue
		}
		if v, err := strconv.ParseFloat(line, 64); err == nil {
			pts = append(pts, v)
		}
	}
	return pts
}

// countVideoPackets returns how many video packets path holds (demux only).
func countVideoPackets(ctx context.Context, path string) (int, error) {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error",
		"-count_packets", "-select_streams", "v:0",
		"-show_entries", "stream=nb_read_packets", "-of", "csv=p=0", path).Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(string(out)), ","))
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

	var chunks []chunkRange
	if pts, perr := probeVideoPTS(ctx, src); perr == nil {
		chunks = planChunksFromPTS(pts, spec.ChunkDuration, vstart)
	}
	if chunks == nil {
		// No usable frame timestamps. Fall back to nominal time ranges, which
		// cannot be verified, so do not trust hardware decode with -ss there.
		chunks = planChunks(total, spec.ChunkDuration, vstart)
		if spec.DecodeBackend != "" {
			spec.DecodeBackend = ""
		}
	}
	if chunks == nil {
		return false, nil
	}
	dir := chunkDir(dst)
	resumed, err := prepareChunkDir(dir, chunkKey(src, spec, chunks))
	if err != nil {
		return false, &OSError{FilePath: src, Err: err}
	}
	// The work directory is kept when the run is interrupted after at least
	// one chunk finished, so the next run resumes instead of starting over.
	// It is removed on success and on any genuine failure.
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()
	interrupted := func(done int, err error) error {
		if ctx.Err() != nil && done > 0 {
			keep = true
		}
		return err
	}

	names := make([]string, len(chunks))
	finished, encodedPrev := 0, false
	for i, c := range chunks {
		names[i] = fmt.Sprintf(encFormat, i)
		final := filepath.Join(dir, names[i])
		length := c.length
		if length == 0 {
			length = total - c.offset
		}
		progress := chunkProgress(src, c.offset, total, onProgress)

		if resumed && c.expect > 0 {
			if n, cerr := countVideoPackets(ctx, final); cerr == nil && n == c.expect {
				if progress != nil {
					progress(ProgressEvent{FilePath: src, Current: length, Total: length})
				}
				finished++
				continue
			}
		}
		if encodedPrev && spec.ChunkCooldown > 0 {
			select {
			case <-ctx.Done():
				return false, interrupted(finished, ctx.Err())
			case <-time.After(spec.ChunkCooldown):
			}
		}
		part := filepath.Join(dir, fmt.Sprintf(partFormat, i))
		_ = os.Remove(final)

		ok, err := encodeChunk(ctx, src, part, c, spec, length, progress)
		if err != nil {
			_ = os.Remove(part)
			return false, interrupted(finished, err)
		}
		if !ok && spec.DecodeBackend != "" {
			// Input -ss is not frame-accurate on some hardware decoders: the
			// chunk came back with the wrong number of frames. Redo it, and
			// the rest of this file, with software decode.
			log.Warn().Str("file", src).Int("chunk", i+1).Str("decode", spec.DecodeBackend).
				Msg("hardware decode returned the wrong frame count for a chunk; redoing it and the remaining chunks with software decode")
			spec.DecodeBackend = ""
			ok, err = encodeChunk(ctx, src, part, c, spec, length, progress)
			if err != nil {
				_ = os.Remove(part)
				return false, interrupted(finished, err)
			}
		}
		if !ok {
			_ = os.Remove(part)
			log.Warn().Str("file", src).Int("chunk", i+1).
				Msg("chunk frame count does not match the source even with software decode; encoding this file in a single pass instead")
			return false, nil
		}
		if err := os.Rename(part, final); err != nil {
			return false, &OSError{FilePath: src, Err: err}
		}
		finished++
		encodedPrev = true
	}

	list := filepath.Join(dir, "list.txt")
	// The concat demuxer resolves entries relative to the list file's own
	// directory, so list bare file names: a relative --src would otherwise
	// double the path.
	spans := make([]time.Duration, len(chunks))
	for i, c := range chunks {
		spans[i] = c.span
	}
	if err := os.WriteFile(list, []byte(concatList(names, spans)), 0o644); err != nil {
		return false, &OSError{FilePath: src, Err: err}
	}
	// Every chunk starts at 0; the join adds the video's original start offset
	// back so it lands where the source had it relative to the audio.
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

// encodeChunk encodes one range and checks it produced exactly the frames the
// source has there. ok=false (with a nil error) means the encode ran but the
// frame count was wrong.
func encodeChunk(ctx context.Context, src, out string, c chunkRange, spec EncodeSpec, length time.Duration, progress func(ProgressEvent)) (ok bool, err error) {
	if err := execFFmpeg(ctx, src, segmentArgs(src, out, c, spec), length, progress); err != nil {
		return false, err
	}
	if c.expect == 0 {
		return true, nil // unplanned from timestamps: nothing to check against
	}
	got, cerr := countVideoPackets(ctx, out)
	if cerr != nil {
		return false, &OSError{FilePath: src, Err: fmt.Errorf("verify chunk: %w", cerr)}
	}
	return got == c.expect, nil
}

// segmentArgs encodes one video-only time range of src with the run's video
// settings. -ss/-t are input options: ffmpeg seeks to the previous keyframe
// and discards up to the exact start, so cuts are frame-accurate. Audio and
// subtitles are handled once, in the final join.
func segmentArgs(src, dst string, c chunkRange, spec EncodeSpec) []string {
	args := []string{"-hide_banner"}
	args = append(args, preInputArgs(spec)...)
	args = append(args, "-ss", fmt.Sprintf("%.6f", c.seek.Seconds()))
	if c.length > 0 {
		args = append(args, "-t", fmt.Sprintf("%.6f", c.length.Seconds()))
	}
	args = append(args, "-i", src, "-map", "0:v:0", "-an", "-sn")
	args = append(args, videoFilterArgs(spec)...)
	args = append(args, rateControlArgs(spec)...)
	args = append(args, hdrArgs(spec)...)
	videoExtra, _ := splitExtraArgs(spec.ExtraArgs)
	args = append(args, videoExtra...)
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
	// Container, metadata and audio arguments belong to the final mux. They
	// come last, as in a single pass, so they override the defaults above.
	_, outputExtra := splitExtraArgs(spec.ExtraArgs)
	args = append(args, outputExtra...)
	return append(args, "-y", dst)
}

// concatList renders the concat demuxer's list. When every chunk has a known
// span, each entry carries an explicit "duration": the demuxer otherwise
// places the next file at this file's own measured duration, which depends on
// encoder delay and frame reordering and can stall a seam by a whole frame
// (measured on B-frame HEVC: 34-134 ms off, against 1 ms with explicit spans).
func concatList(files []string, spans []time.Duration) string {
	explicit := len(spans) == len(files)
	for _, s := range spans {
		if s <= 0 {
			explicit = false
		}
	}
	var b strings.Builder
	for i, f := range files {
		b.WriteString("file '")
		b.WriteString(strings.ReplaceAll(f, "'", `'\''`))
		b.WriteString("'\n")
		if explicit {
			fmt.Fprintf(&b, "duration %.6f\n", spans[i].Seconds())
		}
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
func prepareChunkDir(dir, key string) (resumed bool, err error) {
	marker := filepath.Join(dir, ChunkMarker)
	if _, serr := os.Stat(dir); serr == nil {
		have, merr := os.ReadFile(marker)
		if merr != nil {
			return false, fmt.Errorf("%s exists and is not a smelt work directory; refusing to reuse it", dir)
		}
		if string(have) == key {
			// Same source, settings and plan: keep finished chunks. Drop
			// anything that was mid-write when the earlier run stopped.
			parts, _ := filepath.Glob(filepath.Join(dir, "enc*.part.transcoded.mkv"))
			for _, p := range parts {
				_ = os.Remove(p)
			}
			_ = os.Remove(filepath.Join(dir, "list.txt"))
			return true, nil
		}
		if err := os.RemoveAll(dir); err != nil {
			return false, err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	return false, os.WriteFile(marker, []byte(key), 0o644)
}

// chunkKey fingerprints everything that determines a chunk's content: the
// source file, the video encode settings, and the chunk plan. A work directory
// is only resumed when its key matches, so a changed file or changed settings
// can never be stitched together with stale chunks.
func chunkKey(src string, spec EncodeSpec, chunks []chunkRange) string {
	h := sha256.New()
	put := func(format string, args ...any) { _, _ = fmt.Fprintf(h, format, args...) } // hash.Hash writes never fail
	put("v1|%s|", src)
	if fi, err := os.Stat(src); err == nil {
		put("%d|%d|", fi.Size(), fi.ModTime().UnixNano())
	}
	videoExtra, _ := splitExtraArgs(spec.ExtraArgs)
	hdr, _ := json.Marshal(spec.HDR)
	put("%s|%d|%s|%s|%s|%d|%s|%q|%d|", spec.Codec, spec.CRF, spec.Preset, spec.Encoder, spec.Backend,
		spec.SourceBitDepth, hdr, videoExtra, spec.ChunkDuration)
	for _, c := range chunks {
		put("%d,%d,%d,%d;", c.seek, c.offset, c.length, c.expect)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// splitExtraArgs separates --ffmpeg-arg tokens into those that configure the
// video encode (applied to every chunk) and those that configure the final mux
// - container, metadata and audio/subtitle options - which must be applied
// once, in the join, not per chunk. Flags are classified by name; every
// flag in the output group consumes one value.
func splitExtraArgs(extra []string) (video, output []string) {
	outputBase := map[string]bool{
		"metadata": true, "movflags": true, "disposition": true, "map_metadata": true,
		"map_chapters": true, "fflags": true, "avoid_negative_ts": true, "ignore_unknown": true,
		"ac": true, "ar": true, "af": true, "sample_fmt": true, "channel_layout": true,
		"tag": true, "write_tmcd": true, "brand": true,
	}
	streamScoped := map[string]bool{"b": true, "c": true, "codec": true, "q": true, "profile": true, "filter": true}
	isOutput := func(flag string) bool {
		name := strings.TrimPrefix(flag, "-")
		base, spec, _ := strings.Cut(name, ":")
		if outputBase[base] {
			return true
		}
		return streamScoped[base] && (strings.HasPrefix(spec, "a") || strings.HasPrefix(spec, "s"))
	}
	for i := 0; i < len(extra); i++ {
		a := extra[i]
		if len(a) > 1 && a[0] == '-' && isOutput(a) {
			output = append(output, a)
			if i+1 < len(extra) {
				i++
				output = append(output, extra[i])
			}
			continue
		}
		video = append(video, a)
	}
	return video, output
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
