package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Raina-Hardik/smelt/internal/ffmpeg"
	"github.com/spf13/cobra"
)

var matchCmd = &cobra.Command{
	Use:   "match <file>",
	Short: "Test per-file conditions; exits 0 if all match, 1 if not",
	Long: `Test one or more conditions against a media file.
Exits 0 if ALL specified conditions are true, 1 otherwise.
With no flags, always exits 0.

Intended for use in generated program scripts:

    smelt each --src /mnt/media | while IFS= read -r f; do
        if smelt match "$f" --codec-ne hevc --height-gt 1080; then
            smelt do "$f" transcode --codec h265 --crf 24 -y
        fi
    done

Conditions are AND-combined. The command is intentionally silent on
mismatch so it is safe to use in shell if/elif chains.

Video conditions:
  --codec-eq CODEC    video codec equals (e.g. h264, hevc, av1, vp9)
  --codec-ne CODEC    video codec does not equal
  --height-gt N       frame height greater than N pixels
  --height-lt N       frame height less than N pixels
  --height-ge N       frame height greater than or equal to N pixels
  --height-le N       frame height less than or equal to N pixels
  --width-gt N        frame width greater than N pixels
  --width-lt N        frame width less than N pixels
  --bitrate-gt N      overall bitrate greater than N kbps
  --bitrate-lt N      overall bitrate less than N kbps

Audio conditions:
  --audio-eq CODEC    first audio codec equals (e.g. aac, opus, ac3)
  --audio-ne CODEC    first audio codec does not equal

Container/file conditions:
  --ext-eq EXT        file extension equals (without dot, e.g. mkv)
  --ext-ne EXT        file extension does not equal

Duration conditions:
  --duration-gt N     duration greater than N seconds
  --duration-lt N     duration less than N seconds`,
	Example: `  smelt match movie.mkv --codec-ne hevc
  smelt match movie.mkv --codec-ne hevc --height-gt 1080
  smelt match movie.mkv --audio-ne aac
  smelt match clip.mp4 --duration-lt 60`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE:         runMatch,
}

func init() {
	matchCmd.Flags().String("codec-eq", "", "video codec equals")
	matchCmd.Flags().String("codec-ne", "", "video codec does not equal")
	matchCmd.Flags().Int64("height-gt", 0, "height greater than N")
	matchCmd.Flags().Int64("height-lt", 0, "height less than N")
	matchCmd.Flags().Int64("height-ge", 0, "height >= N")
	matchCmd.Flags().Int64("height-le", 0, "height <= N")
	matchCmd.Flags().Int64("width-gt", 0, "width greater than N")
	matchCmd.Flags().Int64("width-lt", 0, "width less than N")
	matchCmd.Flags().Int64("bitrate-gt", 0, "overall bitrate > N kbps")
	matchCmd.Flags().Int64("bitrate-lt", 0, "overall bitrate < N kbps")
	matchCmd.Flags().String("audio-eq", "", "first audio codec equals")
	matchCmd.Flags().String("audio-ne", "", "first audio codec does not equal")
	matchCmd.Flags().String("ext-eq", "", "file extension equals (without dot)")
	matchCmd.Flags().String("ext-ne", "", "file extension does not equal")
	matchCmd.Flags().Float64("duration-gt", 0, "duration > N seconds")
	matchCmd.Flags().Float64("duration-lt", 0, "duration < N seconds")
	rootCmd.AddCommand(matchCmd)
}

func runMatch(cmd *cobra.Command, args []string) error {
	path := args[0]

	// Determine which probes are needed before calling ffprobe.
	needsProbe := false
	checkFlag := func(name string) bool {
		f := cmd.Flags().Lookup(name)
		return f != nil && f.Changed
	}
	probeFlags := []string{
		"codec-eq", "codec-ne",
		"height-gt", "height-lt", "height-ge", "height-le",
		"width-gt", "width-lt",
		"bitrate-gt", "bitrate-lt",
		"audio-eq", "audio-ne",
		"duration-gt", "duration-lt",
	}
	for _, f := range probeFlags {
		if checkFlag(f) {
			needsProbe = true
			break
		}
	}

	var attrs *ffmpeg.FileAttrs
	if needsProbe {
		var err error
		attrs, err = ffmpeg.ProbeAttrs(cmd.Context(), path)
		if err != nil {
			// Can't probe → treat as no-match (don't error; stay silent).
			os.Exit(1)
		}
	}

	// Extension checks don't need ffprobe.
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")

	matched, err := evalMatch(cmd, attrs, ext)
	if err != nil {
		// Flag parse error — print and exit 2.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if !matched {
		os.Exit(1)
	}
	return nil // exit 0
}

func evalMatch(cmd *cobra.Command, a *ffmpeg.FileAttrs, ext string) (bool, error) {
	check := func(name string) (string, bool) {
		f := cmd.Flags().Lookup(name)
		if f == nil || !f.Changed {
			return "", false
		}
		return f.Value.String(), true
	}
	checkInt := func(name string) (int64, bool) {
		v, ok := check(name)
		if !ok {
			return 0, false
		}
		n, _ := strconv.ParseInt(v, 10, 64)
		return n, true
	}
	checkFloat := func(name string) (float64, bool) {
		v, ok := check(name)
		if !ok {
			return 0, false
		}
		f, _ := strconv.ParseFloat(v, 64)
		return f, true
	}

	if v, ok := check("codec-eq"); ok && a != nil {
		if a.VideoCodec != v {
			return false, nil
		}
	}
	if v, ok := check("codec-ne"); ok && a != nil {
		if a.VideoCodec == v {
			return false, nil
		}
	}
	if v, ok := checkInt("height-gt"); ok && a != nil {
		if a.Height <= v {
			return false, nil
		}
	}
	if v, ok := checkInt("height-lt"); ok && a != nil {
		if a.Height >= v {
			return false, nil
		}
	}
	if v, ok := checkInt("height-ge"); ok && a != nil {
		if a.Height < v {
			return false, nil
		}
	}
	if v, ok := checkInt("height-le"); ok && a != nil {
		if a.Height > v {
			return false, nil
		}
	}
	if v, ok := checkInt("width-gt"); ok && a != nil {
		if a.Width <= v {
			return false, nil
		}
	}
	if v, ok := checkInt("width-lt"); ok && a != nil {
		if a.Width >= v {
			return false, nil
		}
	}
	if v, ok := checkInt("bitrate-gt"); ok && a != nil {
		if a.BitRate <= v {
			return false, nil
		}
	}
	if v, ok := checkInt("bitrate-lt"); ok && a != nil {
		if a.BitRate >= v {
			return false, nil
		}
	}
	if v, ok := check("audio-eq"); ok && a != nil {
		if a.AudioCodec != v {
			return false, nil
		}
	}
	if v, ok := check("audio-ne"); ok && a != nil {
		if a.AudioCodec == v {
			return false, nil
		}
	}
	if v, ok := check("ext-eq"); ok {
		if ext != strings.ToLower(v) {
			return false, nil
		}
	}
	if v, ok := check("ext-ne"); ok {
		if ext == strings.ToLower(v) {
			return false, nil
		}
	}
	if v, ok := checkFloat("duration-gt"); ok && a != nil {
		if a.DurationS <= v {
			return false, nil
		}
	}
	if v, ok := checkFloat("duration-lt"); ok && a != nil {
		if a.DurationS >= v {
			return false, nil
		}
	}
	return true, nil
}
