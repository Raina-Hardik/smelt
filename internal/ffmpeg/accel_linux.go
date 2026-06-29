//go:build linux

package ffmpeg

import (
	"os"
	"path/filepath"
	"strings"
)

// hwPriority is the order --hwaccel=auto tries hardware backends on Linux.
var hwPriority = []string{"nvenc", "qsv", "vaapi", "amf"}

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
