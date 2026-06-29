//go:build darwin

package ffmpeg

// hwPriority is the order --hwaccel=auto tries hardware backends on macOS.
// VideoToolbox is the native Apple hardware encoder (works on both Apple Silicon
// and Intel Macs with supported GPUs). NVENC is included for rare cases where an
// external NVIDIA GPU is present.
var hwPriority = []string{"videotoolbox", "nvenc"}

// vaapiDevice is a no-op on macOS; VAAPI is a Linux-only API.
func vaapiDevice() string { return "" }
