package ffmpeg

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
// working hardware encoder. All (codec, backend) pairs are probed in parallel.
// Falls back to the original codec (software) if nothing has hardware, or if
// hwaccel is not "auto".
func ResolveCodec(ctx context.Context, codec, hwaccel string) string {
	if strings.ToLower(hwaccel) != "auto" {
		return codec
	}
	start := chainIndex(codec)
	if start < 0 {
		return codec // not in the hw-fallback chain (e.g. vp9)
	}
	chain := hwCodecChain[start:]

	// Probe all (codec, backend) pairs in parallel. codecHasHW[i] is set true
	// if any backend works for chain[i]. Codec priority (chain order) is
	// respected when picking the winner after all probes finish.
	codecHasHW := make([]atomic.Bool, len(chain))
	var wg sync.WaitGroup
	for ci, c := range chain {
		for _, b := range hwPriority {
			enc := encoderName(c, b)
			if enc == "" {
				continue
			}
			ci, b, enc := ci, b, enc
			wg.Go(func() {
				if probeEncoder(ctx, enc, b) {
					codecHasHW[ci].Store(true)
				}
			})
		}
	}
	wg.Wait()

	for i, c := range chain {
		if codecHasHW[i].Load() {
			return c
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
// not mean usable — the right GPU/driver must be present). Under "auto", all
// backends are probed in parallel and the highest-priority working one wins.
// Falls back to the software encoder when hardware is unavailable; backend is
// "" for software.
func ResolveEncoder(ctx context.Context, codec, hwaccel string) (encoder, backend string) {
	sw := codecFlag(codec)
	switch strings.ToLower(hwaccel) {
	case "", "none", "software", "sw":
		return sw, ""
	case "auto":
		if enc, be := parallelProbe(ctx, codec, hwPriority); enc != "" {
			return enc, be
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

// parallelProbe probes all backends for codec simultaneously and returns the
// encoder+backend with the highest priority (lowest index in backends) that
// actually works. Returns ("", "") when nothing works.
func parallelProbe(ctx context.Context, codec string, backends []string) (encoder, backend string) {
	// hits[i] is non-nil when backends[i] has a working encoder for codec.
	// Each goroutine writes to a distinct index, so no mutex is needed here.
	type hit struct{ enc, be string }
	hits := make([]*hit, len(backends))

	var wg sync.WaitGroup
	for i, b := range backends {
		enc := encoderName(codec, b)
		if enc == "" {
			continue
		}
		i, b, enc := i, b, enc
		wg.Go(func() {
			if probeEncoder(ctx, enc, b) {
				hits[i] = &hit{enc: enc, be: b}
			}
		})
	}
	wg.Wait()

	for _, h := range hits {
		if h != nil {
			return h.enc, h.be
		}
	}
	return "", ""
}

// encoderName returns the ffmpeg encoder for a codec alias + backend, or "".
func encoderName(codec, backend string) string {
	base, ok := codecBase[strings.ToLower(codec)]
	if !ok {
		return ""
	}
	return base + "_" + strings.ToLower(backend)
}

// probeTimeout caps each hardware-encoder probe. Driver initialisation on
// systems without the matching GPU can hang for tens of seconds; this keeps
// startup fast on headless servers and laptops without a dGPU.
const probeTimeout = 8 * time.Second

// probeEncoder reports (and caches) whether enc can actually encode here, by
// running a tiny real encode. Compiled-in encoders still fail at runtime without
// the matching hardware, so listing `ffmpeg -encoders` is not enough.
//
// The mutex is held only for cache access, not during the ffmpeg call, so
// concurrent probes for different encoders run in parallel.
func probeEncoder(ctx context.Context, enc, backend string) bool {
	probeMu.Lock()
	if v, ok := probeCache[enc]; ok {
		probeMu.Unlock()
		return v
	}
	probeMu.Unlock()

	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

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

	ok := exec.CommandContext(probeCtx, "ffmpeg", args...).Run() == nil

	// Two goroutines may race to write the same key when a cache miss happens
	// concurrently. Both produce the same answer for the same encoder, so the
	// last write is harmless.
	probeMu.Lock()
	probeCache[enc] = ok
	probeMu.Unlock()
	return ok
}
