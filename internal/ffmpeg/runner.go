package ffmpeg

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
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

// Run transcodes src → dst using codec, calling onProgress for each parsed
// progress line. onProgress may be nil. ctx cancellation kills ffmpeg.
func Run(ctx context.Context, src, dst, codec string, onProgress func(ProgressEvent)) error {
	total, _ := probeDuration(ctx, src)

	args := buildArgs(src, dst, codec)
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

func buildArgs(src, dst, codec string) []string {
	return []string{
		"-hide_banner",
		"-i", src,
		"-c:v", codecFlag(codec),
		"-c:a", "copy",
		"-y",
		dst,
	}
}

func codecFlag(codec string) string {
	switch strings.ToLower(codec) {
	case "h265", "hevc":
		return "libx265"
	case "h264", "avc":
		return "libx264"
	case "av1":
		return "libsvtav1"
	case "vp9":
		return "libvpx-vp9"
	default:
		return codec
	}
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
