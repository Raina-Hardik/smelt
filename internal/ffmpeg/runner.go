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
// alongside -crf and has no string preset.
func rateControlArgs(spec EncodeSpec) []string {
	args := []string{"-c:v", videoEncoder(spec)}
	switch spec.Backend {
	case "nvenc":
		args = append(args, "-rc", "vbr", "-cq", strconv.Itoa(spec.CRF))
		if spec.Preset != "" {
			args = append(args, "-preset", spec.Preset)
		}
	case "qsv":
		args = append(args, "-global_quality", strconv.Itoa(spec.CRF))
		if spec.Preset != "" {
			args = append(args, "-preset", spec.Preset)
		}
	case "vaapi":
		args = append(args, "-rc_mode", "CQP", "-qp", strconv.Itoa(spec.CRF))
	case "amf":
		args = append(args, "-rc", "cqp", "-qp_i", strconv.Itoa(spec.CRF), "-qp_p", strconv.Itoa(spec.CRF))
	case "videotoolbox":
		args = append(args, "-q:v", strconv.Itoa(spec.CRF))
	default: // software
		args = append(args, "-crf", strconv.Itoa(spec.CRF))
		switch videoEncoder(spec) {
		case "libx264", "libx265", "libsvtav1":
			if spec.Preset != "" {
				args = append(args, "-preset", spec.Preset)
			}
		case "libvpx-vp9":
			args = append(args, "-b:v", "0")
		}
	}
	return args
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
