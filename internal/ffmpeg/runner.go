package ffmpeg

import (
	"bufio"
	"context"
	"fmt"
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
	Percent  float64 // 0.0–1.0; 0 if total is unknown
}

// EncodeSpec is the resolved input contract for one ffmpeg invocation. It is
// built by the worker from a *config.Config (with any profile already merged),
// keeping this package ignorant of config/viper. Grow this struct rather than
// the Run signature when adding encode options.
type EncodeSpec struct {
	Codec     string   // alias (h264|h265|av1|vp9) or a raw encoder name
	CRF       int      // constant rate factor
	Preset    string   // encoder preset; ignored by codecs that don't take one
	Container string   // output container (e.g. mp4); drives container-specific muxer flags
	Encoder   string   // resolved concrete encoder (e.g. hevc_nvenc); empty → software from Codec
	Backend   string   // hw backend (nvenc|qsv|vaapi|amf|videotoolbox); "" → software
	ExtraArgs []string // raw passthrough args (--ffmpeg-arg / profile extra_args)
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
				onProgress(ev)
			}
		}
	}

	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return &ExecError{
				FilePath: src,
				ExitCode: exitErr.ExitCode(),
				Stderr:   stderrBuf.String(),
			}
		}
		return &OSError{FilePath: src, Err: err}
	}
	return nil
}

func buildArgs(src, dst string, spec EncodeSpec) []string {
	args := []string{"-hide_banner"}
	args = append(args, preInputArgs(spec)...)
	args = append(args, "-i", src)
	args = append(args, videoFilterArgs(spec)...)
	args = append(args, rateControlArgs(spec)...)
	args = append(args, "-c:a", "copy") // audio passthrough is out of scope for v1
	args = append(args, containerArgs(spec)...)
	args = append(args, spec.ExtraArgs...)
	args = append(args, "-y", dst)
	return args
}

// videoEncoder returns the resolved encoder, or the software encoder for the codec.
func videoEncoder(spec EncodeSpec) string {
	if spec.Encoder != "" {
		return spec.Encoder
	}
	return codecFlag(spec.Codec)
}

// preInputArgs are flags that must precede -i (e.g. selecting the VAAPI device).
func preInputArgs(spec EncodeSpec) []string {
	if spec.Backend == "vaapi" {
		return []string{"-vaapi_device", vaapiDevice()}
	}
	return nil
}

// videoFilterArgs uploads frames to GPU memory where the backend requires it.
func videoFilterArgs(spec EncodeSpec) []string {
	if spec.Backend == "vaapi" {
		return []string{"-vf", "format=nv12,hwupload"}
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
		args = append(args, "-global_quality", crf)
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

// ProbeVideoCodec returns the codec_name of the first video stream (e.g.
// "hevc", "h264", "av1", "vp9"), used to smart-skip files already in the target
// codec. Errors propagate so callers can choose not to skip on probe failure.
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
