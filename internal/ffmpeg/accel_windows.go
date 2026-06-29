//go:build windows

package ffmpeg

// hwPriority is the order --hwaccel=auto tries hardware backends on Windows.
// VAAPI is Linux-only and excluded. AMF covers AMD GPUs; QSV covers Intel.
var hwPriority = []string{"nvenc", "qsv", "amf"}

// vaapiDevice is a no-op on Windows; VAAPI is a Linux-only API.
func vaapiDevice() string { return "" }
