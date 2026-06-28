package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// codecBase maps a codec alias to the encoder-name stem hardware backends use
// (e.g. h265 → "hevc_nvenc", "hevc_vaapi").
var codecBase = map[string]string{
	"h265": "hevc",
	"hevc": "hevc",
	"h264": "h264",
	"avc":  "h264",
	"av1":  "av1",
	"vp9":  "vp9",
}

// CodecName maps a codec alias to the ffprobe codec_name it produces (e.g.
// h265 → "hevc"), so a transcode target can be compared against a file's
// current video codec. Returns "" for an unknown alias.
func CodecName(codec string) string {
	return codecBase[strings.ToLower(codec)]
}

// hwPriority is the order --hwaccel=auto tries hardware backends.
var hwPriority = []string{"nvenc", "qsv", "vaapi", "amf"}

var knownHWAccels = map[string]bool{
	"":     true, // unset == none (software)
	"auto": true, "none": true, "software": true, "sw": true,
	"nvenc": true, "qsv": true, "vaapi": true, "amf": true, "videotoolbox": true,
}

// IsKnownHWAccel reports whether s is a recognized --hwaccel mode.
func IsKnownHWAccel(s string) bool { return knownHWAccels[strings.ToLower(s)] }

var (
	probeMu    sync.Mutex
	probeCache = map[string]bool{}
)

// ResolveEncoder picks the concrete video encoder and backend for a codec and
// --hwaccel mode. Hardware encoders are *functionally probed* (compiled-in does
// not mean usable — the right GPU/driver must be present). It falls back to the
// software encoder when hardware is unavailable; backend is "" for software.
func ResolveEncoder(ctx context.Context, codec, hwaccel string) (encoder, backend string) {
	sw := codecFlag(codec)
	switch strings.ToLower(hwaccel) {
	case "", "none", "software", "sw":
		return sw, ""
	case "auto":
		for _, b := range hwPriority {
			if enc := encoderName(codec, b); enc != "" && probeEncoder(ctx, enc, b) {
				return enc, b
			}
		}
		return sw, ""
	default: // explicit backend: use it if it actually works, else software
		b := strings.ToLower(hwaccel)
		if enc := encoderName(codec, b); enc != "" && probeEncoder(ctx, enc, b) {
			return enc, b
		}
		return sw, ""
	}
}

// encoderName returns the ffmpeg encoder for a codec alias + backend, or "".
func encoderName(codec, backend string) string {
	base, ok := codecBase[strings.ToLower(codec)]
	if !ok {
		return ""
	}
	return base + "_" + strings.ToLower(backend)
}

// probeEncoder reports (and caches) whether enc can actually encode here, by
// running a tiny real encode. Compiled-in encoders still fail at runtime without
// the matching hardware, so listing `ffmpeg -encoders` is not enough.
func probeEncoder(ctx context.Context, enc, backend string) bool {
	probeMu.Lock()
	defer probeMu.Unlock()
	if v, ok := probeCache[enc]; ok {
		return v
	}

	args := []string{"-hide_banner", "-loglevel", "error"}
	switch backend {
	case "vaapi":
		args = append(args, "-vaapi_device", vaapiDevice())
	case "qsv":
		// Explicit device init avoids silent probe failures on headless Linux
		// servers where QSV would otherwise fail to auto-select a DRI node.
		args = append(args, "-init_hw_device", "qsv=qsv:hw_any")
	}
	args = append(args, "-f", "lavfi", "-i", "testsrc=s=320x240:d=1")
	if backend == "vaapi" {
		args = append(args, "-vf", "format=nv12,hwupload")
	}
	args = append(args, "-c:v", enc, "-frames:v", "2", "-f", "null", "-")

	ok := exec.CommandContext(ctx, "ffmpeg", args...).Run() == nil
	probeCache[enc] = ok
	return ok
}

// vaapiDevice picks the DRM render node for VAAPI, overridable via env. It
// prefers an Intel or AMD node: NVIDIA render nodes (often renderD128 on a
// hybrid laptop) do not provide a VAAPI encode entrypoint, so a hardcoded
// renderD128 would aim VAAPI at the wrong GPU.
func vaapiDevice() string {
	if d := os.Getenv("SMELT_VAAPI_DEVICE"); d != "" {
		return d
	}
	nodes, _ := filepath.Glob("/dev/dri/renderD*")
	for _, n := range nodes {
		switch strings.TrimSpace(readVendor(n)) {
		case "0x8086", "0x1002": // Intel, AMD
			return n
		}
	}
	return "/dev/dri/renderD128"
}

func readVendor(renderNode string) string {
	b, _ := os.ReadFile("/sys/class/drm/" + filepath.Base(renderNode) + "/device/vendor")
	return string(b)
}
