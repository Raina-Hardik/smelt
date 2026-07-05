package ffmpeg

import (
	"context"
	"os/exec"
	"sync"
)

// decodeShape is the decode-probe cache key. Whether a device can decode a
// stream depends on its codec × profile × pixel format (the device's decode
// blocks), not on the file itself — a library of identically-shaped files
// costs one probe.
type decodeShape struct {
	backend string
	codec   string
	profile string
	pixFmt  string
}

var (
	decodeMu    sync.Mutex
	decodeCache = map[decodeShape]bool{}
)

// runDecodeProbe is the ffmpeg invocation behind ProbeDecode, indirected so
// cache behavior is testable without spawning processes.
var runDecodeProbe = func(ctx context.Context, args ...string) error {
	return exec.CommandContext(ctx, "ffmpeg", args...).Run()
}

// hwDecodeArgs returns the pre-input hwaccel block used both to probe decode
// and to run the full hardware pipeline (they must match, or the probe proves
// nothing). nil marks encode-only backends: AMF (no AMD hardware to validate
// against) and VideoToolbox (no macOS hardware) stay software-decode this
// release, so ProbeDecode refuses them without spawning ffmpeg.
func hwDecodeArgs(backend string) []string {
	switch backend {
	case "vaapi":
		return []string{"-vaapi_device", vaapiDevice(), "-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi"}
	case "qsv":
		return []string{"-init_hw_device", "qsv=qsv:hw_any", "-hwaccel", "qsv", "-hwaccel_output_format", "qsv"}
	case "nvenc":
		return []string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"}
	}
	return nil
}

// HWDecodeCapable reports whether backend has a hardware decode path this
// release (AMF and VideoToolbox are encode-only).
func HWDecodeCapable(backend string) bool { return hwDecodeArgs(backend) != nil }

// SoftwareEncoder returns the software encoder for a codec alias (e.g. h265 →
// libx265), for callers routing a single file back to the software pipeline.
func SoftwareEncoder(codec string) string { return codecFlag(codec) }

// ProbeDecode reports whether backend can hardware-decode src on the same
// device the encoder uses, by actually decoding a few frames of the real file
// (compiled-in hwaccels still fail at runtime without the right decode
// blocks; listings prove nothing). Results are cached per shape. Unlike the
// encoder probe, a *completed* negative is decisive evidence — the device
// lacks the decode block for this shape — and is cached so a large library
// doesn't pay ~1 s per file; a timeout or context cancel is not evidence and
// is never cached.
func ProbeDecode(ctx context.Context, backend, src string, attrs *FileAttrs) bool {
	pre := hwDecodeArgs(backend)
	if pre == nil || attrs == nil {
		return false
	}
	key := decodeShape{backend: backend, codec: attrs.VideoCodec, profile: attrs.Profile, pixFmt: attrs.PixFmt}

	decodeMu.Lock()
	if v, ok := decodeCache[key]; ok {
		decodeMu.Unlock()
		return v
	}
	decodeMu.Unlock()

	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	args := []string{"-hide_banner", "-loglevel", "error"}
	args = append(args, pre...)
	args = append(args, "-i", src, "-map", "0:v:0", "-frames:v", "3", "-an", "-sn", "-f", "null", "-")

	ok := runDecodeProbe(probeCtx, args...) == nil

	if probeCtx.Err() == nil && ctx.Err() == nil {
		decodeMu.Lock()
		decodeCache[key] = ok
		decodeMu.Unlock()
	}
	return ok
}

// EvictDecodeCache drops the cached decode result for src's shape. Called by
// the worker when a hardware-decode run fails at runtime despite a positive
// probe, so subsequent identical files re-probe instead of all paying a
// fail-then-retry cycle.
func EvictDecodeCache(backend string, attrs *FileAttrs) {
	if attrs == nil {
		return
	}
	key := decodeShape{backend: backend, codec: attrs.VideoCodec, profile: attrs.Profile, pixFmt: attrs.PixFmt}
	decodeMu.Lock()
	delete(decodeCache, key)
	decodeMu.Unlock()
}
