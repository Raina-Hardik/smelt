package ffmpeg

import (
	"context"
	"os/exec"
	"strings"
	"sync"
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
	//
	// Early exit: once chain[0] (highest-priority codec) gets a hit we cancel
	// every other probe — nothing in the chain can beat it.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		ci  int
		hit bool
	}

	spawned := 0
	ch := make(chan result, len(chain)*len(hwPriority))

	for ci, c := range chain {
		for _, b := range hwPriority {
			enc := encoderName(c, b)
			if enc == "" {
				continue
			}
			ci, b, enc := ci, b, enc
			spawned++
			go func() {
				ch <- result{ci: ci, hit: probeEncoder(ctx, enc, b)}
			}()
		}
	}

	codecHasHW := make([]bool, len(chain))
	for range spawned {
		r := <-ch
		if r.hit {
			codecHasHW[r.ci] = true
			if r.ci == 0 {
				cancel() // highest-priority codec has hardware; stop waiting
				return chain[0]
			}
		}
	}

	for i, c := range chain {
		if codecHasHW[i] {
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
//
// Early exit: once a result arrives for the highest-priority backend (index 0)
// the remaining probes are cancelled — there is nothing better to wait for.
// Probes cancelled this way are not cached so a later probe (e.g. after the
// user switches hwaccel in the TUI) gets a fresh result.
func parallelProbe(ctx context.Context, codec string, backends []string) (encoder, backend string) {
	type result struct {
		idx     int // -1 = failed/cancelled
		enc, be string
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Count only the backends that have an encoder for this codec.
	spawned := 0
	ch := make(chan result, len(backends))

	for i, b := range backends {
		enc := encoderName(codec, b)
		if enc == "" {
			continue
		}
		i, b, enc := i, b, enc
		spawned++
		go func() {
			if probeEncoder(ctx, enc, b) {
				ch <- result{idx: i, enc: enc, be: b}
			} else {
				ch <- result{idx: -1}
			}
		}()
	}

	bestIdx := len(backends) // sentinel: no winner yet
	bestEnc, bestBe := "", ""

	for range spawned {
		r := <-ch
		if r.idx >= 0 && r.idx < bestIdx {
			bestIdx = r.idx
			bestEnc, bestBe = r.enc, r.be
			if bestIdx == 0 {
				cancel() // index 0 is the highest priority; nothing can beat it
				return bestEnc, bestBe
			}
		}
	}
	return bestEnc, bestBe
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
func probeEncoder(ctx context.Context, enc, backend string) bool {
	return probeEncodeDepth(ctx, enc, backend, 8)
}

// ProbeEncoder10Bit reports (and caches, key "<enc>:main10") whether enc can
// encode 10-bit on this device, probed with a p010 lavfi source plus the
// encoder family's 10-bit profile flag. Gates the hardware pipeline for
// 10-bit sources: a device that decodes main10 but can't encode it must not
// silently flatten the file to 8-bit — the worker routes such files through
// the full software pipeline instead.
func ProbeEncoder10Bit(ctx context.Context, enc, backend string) bool {
	return probeEncodeDepth(ctx, enc, backend, 10)
}

// probeEncodeDepth is the shared probe body. The mutex is held only for cache
// access, not during the ffmpeg call, so concurrent probes for different
// encoders run in parallel.
func probeEncodeDepth(ctx context.Context, enc, backend string, bitDepth int) bool {
	key := enc
	if bitDepth == 10 {
		key = enc + ":main10"
	}
	probeMu.Lock()
	if v, ok := probeCache[key]; ok {
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
	// QSV/NVENC take 10-bit system-memory frames directly; only VAAPI needs
	// the explicit upload (both shapes validated on real hardware).
	switch {
	case backend == "vaapi" && bitDepth == 10:
		args = append(args, "-vf", "format=p010,hwupload")
	case backend == "vaapi":
		args = append(args, "-vf", "format=nv12,hwupload")
	case bitDepth == 10:
		args = append(args, "-vf", "format=p010")
	}
	args = append(args, "-c:v", enc)
	if bitDepth == 10 {
		args = append(args, tenBitProfileArgs(enc)...)
	}
	args = append(args, "-frames:v", "2", "-f", "null", "-")

	ok := exec.CommandContext(probeCtx, "ffmpeg", args...).Run() == nil

	// Only cache successful probes. A failure might be transient — the NVENC
	// driver briefly unavailable after a sibling probe just finished, a timeout
	// caused by driver initialisation under load, etc. Caching false would lock
	// the encoder out for the entire TUI session, causing silent software
	// fallback on the next probe attempt. Absence of a cache entry means "retry".
	//
	// ctx cancellation (parent cancelled because a higher-priority probe already
	// won) is not evidence of absence either — skip those regardless.
	if ok && ctx.Err() == nil {
		probeMu.Lock()
		probeCache[key] = true
		probeMu.Unlock()
	}
	return ok
}
