//go:build integration

package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Hardware chunking checks. They need a real GPU, so they only run when asked:
//
//	SMELT_HW_TEST=qsv   go test -tags integration -run Hardware ./internal/ffmpeg
//	SMELT_HW_TEST=vaapi go test -tags integration -run Hardware ./internal/ffmpeg
//
// Input -ss is not frame-accurate on some hardware decoders (Intel QSV returns
// extra frames), so runChunked verifies each chunk's frame count and redoes a
// miscounted one with software decode. Whatever the decoder does, the final
// output must have exactly the source's frames and cadence.
func TestHardwareChunkedExactIntegration(t *testing.T) {
	backend := os.Getenv("SMELT_HW_TEST")
	if backend == "" {
		t.Skip("set SMELT_HW_TEST=qsv|vaapi to run on real hardware")
	}
	ctx := context.Background()
	enc := "hevc_" + backend
	if !probeEncoder(ctx, enc, backend) {
		t.Skipf("%s is not usable on this machine", enc)
	}

	dir := t.TempDir()
	for _, depth := range []string{"yuv420p", "yuv420p10le"} {
		t.Run(depth, func(t *testing.T) {
			src := filepath.Join(dir, depth+".mkv")
			codec := "libx264"
			if depth == "yuv420p10le" {
				codec = "libx265"
			}
			gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
				"-f", "lavfi", "-i", "testsrc2=s=1280x720:d=40:r=30000/1001", "-f", "lavfi", "-i", "sine=d=40",
				"-pix_fmt", depth, "-c:v", codec, "-preset", "ultrafast", "-c:a", "aac", src)
			if err := gen.Run(); err != nil {
				t.Skipf("cannot generate test media: %v", err)
			}
			bits := 8
			if depth == "yuv420p10le" {
				bits = 10
				if !ProbeEncoder10Bit(ctx, enc, backend) {
					t.Skipf("%s cannot encode 10-bit here", enc)
				}
			}
			spec := EncodeSpec{Codec: "h265", Encoder: enc, Backend: backend, DecodeBackend: backend,
				SourceBitDepth: bits, CRF: 28, SubtitleMode: "drop", ChunkDuration: 10 * time.Second}
			dst := filepath.Join(dir, depth+".transcoded.mkv")
			if err := Run(ctx, src, dst, spec, nil); err != nil {
				t.Fatalf("hardware chunked run: %v", err)
			}
			if a, b := framesOf(t, src), framesOf(t, dst); a != b {
				t.Errorf("frames: source=%s output=%s", a, b)
			}
			out := videoPTS(t, dst)
			if g := maxGap(out); g > 0.045 {
				t.Errorf("largest frame gap = %.1f ms, want ~33 ms", g*1000)
			}
			pix, _ := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=pix_fmt", "-of", "csv=p=0", dst).Output()
			if !strings.Contains(string(pix), strings.TrimSuffix(depth, "le")) && depth == "yuv420p10le" && !strings.Contains(string(pix), "10") {
				t.Errorf("10-bit source was flattened: pix_fmt=%s", strings.TrimSpace(string(pix)))
			}
		})
	}
}
