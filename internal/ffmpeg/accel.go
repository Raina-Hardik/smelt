package ffmpeg

import (
	"context"
	"os/exec"
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

// hwCodecChain is the preferred codec order for --hwaccel=auto when the
// requested codec has no hardware encoder: try AV1 first (best compression),
// then HEVC, then H.264. Only codecs in this list participate in the fallback.
var hwCodecChain = []string{"av1", "hevc", "h264"}

// ResolveCodec returns the best-quality codec that has a hardware encoder for
// the given hwaccel mode. Under "auto", if the requested codec cannot be
// hardware-encoded it walks av1 → hevc → h264 and returns the first with a
// working hardware encoder. Falls back to the original codec (software) if
// nothing has hardware, or if hwaccel is not "auto".
func ResolveCodec(ctx context.Context, codec, hwaccel string) string {
	if strings.ToLower(hwaccel) != "auto" {
		return codec
	}
	start := chainIndex(codec)
	if start < 0 {
		return codec // not in the hw-fallback chain (e.g. vp9)
	}
	for _, c := range hwCodecChain[start:] {
		for _, b := range hwPriority {
			if enc := encoderName(c, b); enc != "" && probeEncoder(ctx, enc, b) {
				return c
			}
		}
	}
	return codec // software fallback with original codec
}

func chainIndex(codec string) int {
	base := codecBase[strings.ToLower(codec)]
	for i, c := range hwCodecChain {
		if codecBase[c] == base {
			return i
		}
	}
	return -1
}

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

