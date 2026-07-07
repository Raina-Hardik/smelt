package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ProgressEvent carries live transcoding progress for a single file.
type ProgressEvent struct {
	FilePath string
	Current  time.Duration
	Total    time.Duration
	Percent  float64       // 0.0–1.0; 0 if total is unknown
	Speed    float64       // ffmpeg's realtime multiplier (e.g. 1.02 for "1.02x"); 0 if unknown
	ETA      time.Duration // estimated time remaining; 0 if Total or Speed is unknown
}

// EncodeSpec is the resolved input contract for one ffmpeg invocation. It is
// built by the worker from a *config.Config (with any profile already merged),
// keeping this package ignorant of config/viper. Grow this struct rather than
// the Run signature when adding encode options.
type EncodeSpec struct {
	Codec         string   // alias (h264|h265|av1|vp9) or a raw encoder name
	CRF           int      // constant rate factor
	Preset        string   // encoder preset; ignored by codecs that don't take one
	Container     string   // output container (e.g. mp4); drives container-specific muxer flags
	Encoder       string   // resolved concrete encoder (e.g. hevc_nvenc); empty → software from Codec
	Backend       string   // hw backend (nvenc|qsv|vaapi|amf|videotoolbox); "" → software
	AudioCodec    string   // audio codec alias (aac|opus|…) or "copy"/"" to stream-copy
	AudioBitrate  string   // e.g. "192k"; applied only when re-encoding audio
	SubtitleMode  string   // "copy" (default) | "drop"; controls subtitle stream handling
	DecodeThreads int      // 0 = ffmpeg default; caps the decoder's thread count (pre -i, unlike --ffmpeg-arg)
	ExtraArgs     []string // raw passthrough args (--ffmpeg-arg / profile extra_args)

	// DecodeBackend selects hardware decode: "" = software decode; otherwise it
	// must equal Backend (same-device invariant — decode happens on the exact
	// device the encoder uses; frames never cross a device boundary).
	DecodeBackend string
	// SourceBitDepth is 10 for 10-bit sources riding the hardware pipeline
	// (drives p010 upload and the encoder's main10 profile flag); 0/8 otherwise.
	SourceBitDepth int
}

// ExecError means ffmpeg ran but exited non-zero.
type ExecError struct {
	FilePath string
	ExitCode int
	Stderr   string
}

func (e *ExecError) Error() string {
	return fmt.Sprintf("ffmpeg exit %d for %q: %s", e.ExitCode, e.FilePath, truncate(e.Stderr, 200))
}

// OSError wraps failures that happen before or after ffmpeg executes.
type OSError struct {
	FilePath string
	Err      error
}

func (e *OSError) Error() string { return fmt.Sprintf("OS error for %q: %v", e.FilePath, e.Err) }
func (e *OSError) Unwrap() error { return e.Err }

var timeRe = regexp.MustCompile(`time=(\d{2}):(\d{2}):(\d{2})\.(\d{2})`)
var speedRe = regexp.MustCompile(`speed=\s*([\d.]+)x`)

// Run transcodes src → dst using spec, calling onProgress for each parsed
// progress line. onProgress may be nil. ctx cancellation kills ffmpeg.
func Run(ctx context.Context, src, dst string, spec EncodeSpec, onProgress func(ProgressEvent)) error {
	total, _ := probeDuration(ctx, src)

	args := buildArgs(src, dst, spec)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return &OSError{FilePath: src, Err: err}
	}
	if err := cmd.Start(); err != nil {
		return &OSError{FilePath: src, Err: err}
	}

	var stderrBuf strings.Builder
	sc := bufio.NewScanner(stderr)
	sc.Split(scanCRLF)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		stderrBuf.WriteString(line)
		stderrBuf.WriteByte('\n')

		if onProgress != nil {
			if cur, ok := parseTime(line); ok {
				ev := ProgressEvent{FilePath: src, Current: cur, Total: total}
				if total > 0 {
					ev.Percent = float64(cur) / float64(total)
					if ev.Percent > 1 {
						ev.Percent = 1
					}
				}
				if speed, ok := parseSpeed(line); ok {
					ev.Speed = speed
					if total > cur && speed > 0 {
						ev.ETA = time.Duration(float64(total-cur) / speed)
					}
				}
				onProgress(ev)
			}
		}
	}

	scanErr := sc.Err()
	if scanErr != nil {
		// sc.Scan() gave up early (e.g. a still-oversized token). Drain
		// whatever ffmpeg has left to write so it never blocks on a full
		// stderr pipe before we reap it below.
		_, _ = io.Copy(io.Discard, stderr)
	}

	waitErr := cmd.Wait()

	if scanErr != nil {
		return &OSError{FilePath: src, Err: fmt.Errorf("reading ffmpeg stderr: %w", scanErr)}
	}

	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			return &ExecError{
				FilePath: src,
				ExitCode: exitErr.ExitCode(),
				Stderr:   stderrBuf.String(),
			}
		}
		return &OSError{FilePath: src, Err: waitErr}
	}
	return nil
}

// scanCRLF is bufio.ScanLines adapted to split on a bare '\r' as well as
// '\n': ffmpeg rewrites its progress line in place using '\r' with no
// trailing '\n', so the default split never terminates a token and it grows
// until it exceeds bufio's max token size, at which point Scan stops
// silently and the caller stops draining stderr — deadlocking ffmpeg on a
// full pipe. Splitting on either byte turns each progress update into its
// own small token.
func scanCRLF(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[0:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func buildArgs(src, dst string, spec EncodeSpec) []string {
	args := []string{"-hide_banner"}
	args = append(args, preInputArgs(spec)...)
	args = append(args, "-i", src)
	args = append(args, streamMapArgs(spec)...)
	args = append(args, videoFilterArgs(spec)...)
	args = append(args, rateControlArgs(spec)...)
	args = append(args, audioArgs(spec)...)
	args = append(args, containerArgs(spec)...)
	args = append(args, spec.ExtraArgs...)
	args = append(args, "-y", dst)
	return args
}

// streamMapArgs emits the -map flags that control which streams are included in
// the output. We always select the primary video stream and all audio streams so
// multi-track files are preserved. Subtitles are copied by default and dropped
// with --subs=drop (or when the output container does not support them, handled
// at the user level via --subs).
func streamMapArgs(spec EncodeSpec) []string {
	args := []string{
		"-map", "0:v:0", // primary video stream only (avoids multi-video-stream edge cases)
		"-map", "0:a", // all audio streams (preserves multi-track/multi-lang)
	}
	if strings.EqualFold(spec.SubtitleMode, "drop") {
		args = append(args, "-sn")
	} else {
		args = append(args, "-map", "0:s?", "-c:s", "copy") // optional: no error if no sub streams
	}
	return args
}

// videoEncoder returns the resolved encoder, or the software encoder for the codec.
func videoEncoder(spec EncodeSpec) string {
	if spec.Encoder != "" {
		return spec.Encoder
	}
	return codecFlag(spec.Codec)
}

// preInputArgs are flags that must precede -i: the hardware decode block, the
// encoder's device selection, and the decoder thread cap. With hardware decode
// active (DecodeBackend != "", always equal to Backend), decoded frames stay
// in device memory and feed the encoder directly; -threads is pointless there
// and omitted for a clean command line — it still applies on every
// software-decode path.
func preInputArgs(spec EncodeSpec) []string {
	if spec.DecodeBackend != "" {
		switch spec.DecodeBackend {
		case "vaapi":
			return []string{"-vaapi_device", vaapiDevice(), "-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi"}
		case "qsv":
			return []string{"-init_hw_device", "qsv=qsv:hw_any", "-hwaccel", "qsv", "-hwaccel_output_format", "qsv"}
		case "nvenc":
			return []string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"}
		}
	}
	var args []string
	if spec.DecodeThreads > 0 {
		args = append(args, "-threads", strconv.Itoa(spec.DecodeThreads))
	}
	switch spec.Backend {
	case "vaapi":
		args = append(args, "-vaapi_device", vaapiDevice())
	case "qsv":
		args = append(args, "-init_hw_device", "qsv=qsv:hw_any")
	}
	return args
}

// videoFilterArgs uploads frames to GPU memory where the backend requires it.
// With hardware decode, frames are already device surfaces — no filter. The
// software-decode VAAPI upload is bit-depth aware: nv12 (8-bit) / p010
// (10-bit), so 10-bit sources are not silently flattened to 8-bit.
func videoFilterArgs(spec EncodeSpec) []string {
	if spec.DecodeBackend != "" {
		return nil
	}
	if spec.Backend == "vaapi" {
		if spec.SourceBitDepth == 10 {
			return []string{"-vf", "format=p010,hwupload"}
		}
		return []string{"-vf", "format=nv12,hwupload"}
	}
	return nil
}

// tenBitProfileArgs is the per-encoder-family flag that selects a 10-bit
// encode profile, keyed by the probed truth: hevc encoders need an explicit
// main10; av1 hardware encoders take 10-bit input with no -profile:v at all
// (validated on av1_nvenc / av1_qsv / av1_vaapi).
func tenBitProfileArgs(encoder string) []string {
	if strings.HasPrefix(encoder, "hevc_") {
		return []string{"-profile:v", "main10"}
	}
	return nil
}

// containerArgs adds muxer flags that make a container "just work". For mp4 it
// enables faststart (web streaming) and tags HEVC as hvc1 for Apple compatibility.
func containerArgs(spec EncodeSpec) []string {
	switch strings.ToLower(spec.Container) {
	case "mp4", "m4v", "mov":
		args := []string{"-movflags", "+faststart"}
		if codecFlag(spec.Codec) == "libx265" {
			args = append(args, "-tag:v", "hvc1")
		}
		return args
	default:
		return nil
	}
}

// rateControlArgs builds the video-encoder flags. Rate control differs per
// encoder: x264/x265/SVT-AV1 take -crf (+ -preset); libvpx-vp9 needs -b:v 0
// alongside -crf and has no string preset. The preset is translated into the
// target encoder's namespace (see encoderPreset) rather than forwarded raw.
func rateControlArgs(spec EncodeSpec) []string {
	enc := videoEncoder(spec)
	args := []string{"-c:v", enc}
	crf := strconv.Itoa(spec.CRF)
	switch spec.Backend {
	case "nvenc":
		args = append(args, "-rc", "vbr", "-cq", crf)
	case "qsv":
		args = append(args, "-q:v", crf) // CQP; ICQ (-global_quality) unsupported on integrated Intel GPUs
	case "vaapi":
		args = append(args, "-rc_mode", "CQP", "-qp", crf)
	case "amf":
		args = append(args, "-rc", "cqp", "-qp_i", crf, "-qp_p", crf)
	case "videotoolbox":
		args = append(args, "-q:v", crf)
	default: // software
		args = append(args, "-crf", crf)
		if enc == "libvpx-vp9" {
			args = append(args, "-b:v", "0")
		}
	}
	if spec.Backend != "" && spec.SourceBitDepth == 10 {
		args = append(args, tenBitProfileArgs(enc)...)
	}
	if p := encoderPreset(spec.Backend, enc, spec.Preset); p != "" {
		args = append(args, "-preset", p)
	}
	return args
}

// encoderPreset maps a requested preset into the namespace the target encoder
// accepts, returning "" to omit -preset (use the encoder default) when there is
// no sensible mapping. ffmpeg rejects cross-family preset names — e.g. x264's
// "superfast" on NVENC, or any name on SVT-AV1 which wants a number — so we
// never forward an incompatible value.
func encoderPreset(backend, encoder, preset string) string {
	if preset == "" {
		return ""
	}
	switch backend {
	case "nvenc":
		return nvencPreset(preset)
	case "qsv":
		return qsvPreset(preset)
	case "":
		switch encoder {
		case "libx264", "libx265": // native x264-family namespace
			return preset
		case "libsvtav1": // wants an integer 0–13
			if isNumeric(preset) {
				return preset
			}
			return svtPreset(preset)
		}
	}
	return "" // vaapi/amf/videotoolbox/vp9: no -preset
}

// nvencPreset maps x264-style speed names onto NVENC's named presets and passes
// NVENC-native presets (p1–p7, hq, ll, …) through unchanged.
func nvencPreset(p string) string {
	switch p {
	case "ultrafast", "superfast", "veryfast", "faster", "fast":
		return "fast"
	case "medium":
		return "medium"
	case "slow", "slower", "veryslow":
		return "slow"
	}
	return p
}

// qsvPreset maps the two x264 names QSV lacks; the rest (veryfast…veryslow) are
// already valid QSV presets.
func qsvPreset(p string) string {
	switch p {
	case "ultrafast", "superfast":
		return "veryfast"
	}
	return p
}

// svtPreset maps x264-style names to SVT-AV1 preset numbers (0 slowest – 13
// fastest); an unknown name yields "" so the encoder uses its default.
func svtPreset(p string) string {
	switch p {
	case "ultrafast":
		return "12"
	case "superfast":
		return "11"
	case "veryfast":
		return "10"
	case "faster":
		return "9"
	case "fast":
		return "8"
	case "medium":
		return "6"
	case "slow":
		return "4"
	case "slower":
		return "3"
	case "veryslow":
		return "2"
	}
	return ""
}

// Preset menus offered for each encoder family, fastest→slowest, plus a sane
// default. The TUI shows only the set valid for the resolved encoder; CLI input
// is still normalized by encoderPreset. nil means the encoder takes no -preset.
var (
	x264Presets  = []string{"ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow", "slower", "veryslow"}
	nvencPresets = []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7"}
	qsvPresets   = []string{"veryfast", "faster", "fast", "medium", "slow", "slower", "veryslow"}
	svtPresets   = []string{"2", "4", "6", "8", "10", "12"}
)

// PresetsFor returns the presets smelt offers for a resolved encoder/backend, or
// nil when the encoder takes no -preset (vp9, vaapi, amf, videotoolbox).
func PresetsFor(backend, encoder string) []string {
	switch backend {
	case "nvenc":
		return nvencPresets
	case "qsv":
		return qsvPresets
	case "":
		switch encoder {
		case "libx264", "libx265":
			return x264Presets
		case "libsvtav1":
			return svtPresets
		}
	}
	return nil
}

// DefaultPreset is the preset to fall back to when the current one isn't valid
// for a (newly resolved) encoder. "" when the encoder takes no preset.
func DefaultPreset(backend, encoder string) string {
	switch backend {
	case "nvenc":
		return "p5"
	case "qsv":
		return "medium"
	case "":
		switch encoder {
		case "libx264", "libx265":
			return "medium"
		case "libsvtav1":
			return "8"
		}
	}
	return ""
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// audioArgs builds the audio-stream flags. The default (copy/"") stream-copies
// the audio untouched; any other codec re-encodes, optionally at -b:a bitrate.
func audioArgs(spec EncodeSpec) []string {
	if spec.AudioCodec == "" || strings.EqualFold(spec.AudioCodec, "copy") {
		return []string{"-c:a", "copy"}
	}
	args := []string{"-c:a", audioCodecFlag(spec.AudioCodec)}
	if spec.AudioBitrate != "" {
		args = append(args, "-b:a", spec.AudioBitrate)
	}
	return args
}

// audioCodecAliases maps user-facing audio codec names to ffmpeg encoders.
var audioCodecAliases = map[string]string{
	"copy": "copy",
	"aac":  "aac",
	"opus": "libopus",
	"mp3":  "libmp3lame",
	"ac3":  "ac3",
	"flac": "flac",
}

func audioCodecFlag(codec string) string {
	if enc, ok := audioCodecAliases[strings.ToLower(codec)]; ok {
		return enc
	}
	return codec // passthrough for raw encoder names
}

// IsKnownAudioCodec reports whether codec is a recognized audio alias.
func IsKnownAudioCodec(codec string) bool {
	_, ok := audioCodecAliases[strings.ToLower(codec)]
	return ok
}

// KnownAudioCodecs returns the recognized audio aliases, sorted.
func KnownAudioCodecs() []string {
	out := make([]string, 0, len(audioCodecAliases))
	for k := range audioCodecAliases {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// codecAliases maps user-facing codec names to ffmpeg software encoders.
var codecAliases = map[string]string{
	"h265": "libx265",
	"hevc": "libx265",
	"h264": "libx264",
	"avc":  "libx264",
	"av1":  "libsvtav1",
	"vp9":  "libvpx-vp9",
}

func codecFlag(codec string) string {
	if enc, ok := codecAliases[strings.ToLower(codec)]; ok {
		return enc
	}
	return codec // passthrough for raw encoder names
}

// IsKnownCodec reports whether codec is a recognized alias.
func IsKnownCodec(codec string) bool {
	_, ok := codecAliases[strings.ToLower(codec)]
	return ok
}

// KnownCodecs returns the recognized codec aliases, sorted.
func KnownCodecs() []string {
	out := make([]string, 0, len(codecAliases))
	for k := range codecAliases {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func parseTime(line string) (time.Duration, bool) {
	m := timeRe.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	h, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	s, _ := strconv.Atoi(m[3])
	cs, _ := strconv.Atoi(m[4])
	d := time.Duration(h)*time.Hour +
		time.Duration(min)*time.Minute +
		time.Duration(s)*time.Second +
		time.Duration(cs)*10*time.Millisecond
	return d, true
}

func parseSpeed(line string) (float64, bool) {
	m := speedRe.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	speed, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return speed, true
}

// ProbeVideoCodec returns the codec_name of the first video stream (e.g.
// "hevc", "h264", "av1", "vp9"), used to smart-skip files already in the target
// codec. Errors propagate so callers can choose not to skip on probe failure.
// CheckDeps returns an error if ffmpeg or ffprobe are not found on PATH.
func CheckDeps() error {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%s not found on PATH — install ffmpeg (https://ffmpeg.org/download.html)", bin)
		}
	}
	return nil
}

// FileHealth summarises the result of a quick ffprobe health check.
type FileHealth struct {
	VideoCodec string
	HasVideo   bool
	Duration   time.Duration
}

// ProbeHealth runs ffprobe against path and returns basic stream information.
// An error means ffprobe could not open the file — i.e. the file is corrupt or
// unreadable. A nil error with HasVideo=false means the container is valid but
// carries no video stream (e.g. audio-only).
func ProbeHealth(ctx context.Context, path string) (*FileHealth, error) {
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "stream=codec_type,codec_name:format=duration",
		"-of", "default=noprint_wrappers=1",
		path,
	).Output()
	if err != nil {
		return nil, err
	}
	info := &FileHealth{}
	var lastType string
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "codec_type":
			lastType = v
		case "codec_name":
			if lastType == "video" && !info.HasVideo {
				info.HasVideo = true
				info.VideoCodec = v
			}
		case "duration":
			if secs, err := strconv.ParseFloat(v, 64); err == nil {
				info.Duration = time.Duration(secs * float64(time.Second))
			}
		}
	}
	return info, nil
}

// ProbeDolbyVision reports whether the primary video stream carries a Dolby
// Vision configuration record (RPU side data). smelt has no DV passthrough —
// a plain re-encode silently drops the RPU layer — so this gates the
// --i-know-this-drops-hdr safety check rather than feeding an encode
// decision. Errors propagate so callers can choose whether to fail closed or
// open on an unreadable file.
func ProbeDolbyVision(ctx context.Context, path string) (bool, error) {
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream_side_data=side_data_type",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	).Output()
	if err != nil {
		return false, err
	}
	return strings.Contains(string(out), "DOVI configuration record"), nil
}

func ProbeVideoCodec(ctx context.Context, path string) (string, error) {
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func probeDuration(ctx context.Context, path string) (time.Duration, error) {
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	).Output()
	if err != nil {
		return 0, err
	}
	secs, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(secs * float64(time.Second)), nil
}

// FileAttrs carries per-file attributes used by the smelt match command and
// the per-file hardware-decode decision (codec × profile × pix_fmt is the
// decode-probe cache key; BitDepth drives nv12/p010 and main10 selection).
type FileAttrs struct {
	VideoCodec string
	AudioCodec string
	Profile    string // video profile as ffprobe reports it (e.g. "Main 10", "High")
	PixFmt     string // video pixel format (e.g. yuv420p, yuv420p10le)
	BitDepth   int    // bits per component derived from PixFmt (8 when unsuffixed)
	Height     int64
	Width      int64
	BitRate    int64 // kbps, overall container bitrate
	DurationS  float64
}

// pixFmtDepthRe captures the bit-depth suffix ffmpeg pixel formats carry
// directly before their endianness marker (yuv420p10le → 10, p016le → 16).
var pixFmtDepthRe = regexp.MustCompile(`(\d{1,2})(le|be)$`)

// bitDepthFromPixFmt derives bits per component from a pixel format name.
// Formats without a depth+endianness suffix (yuv420p, nv12, …) are 8-bit.
func bitDepthFromPixFmt(pixFmt string) int {
	if pixFmt == "" {
		return 0
	}
	if m := pixFmtDepthRe.FindStringSubmatch(pixFmt); m != nil {
		d, _ := strconv.Atoi(m[1])
		return d
	}
	return 8
}

// HWPipelineBitDepth returns 8 or 10 for the 4:2:0 pixel formats the hardware
// pipeline supports, and 0 for everything else (12-bit, 4:2:2, 4:4:4, exotic
// formats) — those always take the software pipeline; no p010 mapping is
// attempted for them.
func HWPipelineBitDepth(pixFmt string) int {
	switch pixFmt {
	case "yuv420p", "yuvj420p", "nv12", "nv21":
		return 8
	case "yuv420p10le", "p010le":
		return 10
	}
	return 0
}

// ProbeAttrs queries a file for the attributes used by smelt match and the
// hardware-decode decision.
func ProbeAttrs(ctx context.Context, path string) (*FileAttrs, error) {
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "stream=codec_type,codec_name,profile,pix_fmt,height,width:format=bit_rate,duration",
		"-of", "default=noprint_wrappers=1",
		path,
	).Output()
	if err != nil {
		return nil, err
	}
	return parseAttrs(string(out)), nil
}

// parseAttrs consumes ffprobe's default-format output. ffprobe prints a
// stream's codec_name and profile *before* its codec_type line, so those two
// are buffered per stream and committed once the type is known; the fields
// printed after codec_type (pix_fmt, width, height) key off it directly.
func parseAttrs(out string) *FileAttrs {
	a := &FileAttrs{}
	var lastType, pendingName, pendingProfile string
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "codec_name":
			pendingName = v
		case "profile":
			pendingProfile = v
		case "codec_type":
			lastType = v
			if v == "video" && a.VideoCodec == "" {
				a.VideoCodec = pendingName
				a.Profile = pendingProfile
			} else if v == "audio" && a.AudioCodec == "" {
				a.AudioCodec = pendingName
			}
			pendingName, pendingProfile = "", ""
		case "pix_fmt":
			if lastType == "video" && a.PixFmt == "" {
				a.PixFmt = v
				a.BitDepth = bitDepthFromPixFmt(v)
			}
		case "height":
			if lastType == "video" {
				a.Height, _ = strconv.ParseInt(v, 10, 64)
			}
		case "width":
			if lastType == "video" {
				a.Width, _ = strconv.ParseInt(v, 10, 64)
			}
		case "bit_rate":
			if br, err := strconv.ParseInt(v, 10, 64); err == nil {
				a.BitRate = br / 1000
			}
		case "duration":
			a.DurationS, _ = strconv.ParseFloat(v, 64)
		}
	}
	return a
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
