package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

// HDR transfer characteristics, as ffprobe/ffmpeg name them.
const (
	transferPQ  = "smpte2084"    // HDR10 / HDR10+
	transferHLG = "arib-std-b67" // HLG
)

// MasteringDisplay is the SMPTE ST 2086 mastering display colour volume.
// Chromaticities are CIE 1931 xy coordinates; luminance is in cd/m².
type MasteringDisplay struct {
	RedX, RedY     float64
	GreenX, GreenY float64
	BlueX, BlueY   float64
	WhiteX, WhiteY float64
	MinLum, MaxLum float64
}

// HDRInfo is the HDR signalling carried by a source's video stream: the
// colour tags plus, for HDR10, the static mastering-display and content-light
// metadata. A nil *HDRInfo means the source is SDR.
type HDRInfo struct {
	ColorPrimaries string // e.g. bt2020
	ColorTransfer  string // smpte2084 (PQ) or arib-std-b67 (HLG)
	ColorSpace     string // e.g. bt2020nc
	ColorRange     string // tv | pc

	Mastering *MasteringDisplay // nil when the source carries none
	MaxCLL    int               // MaxCLL in cd/m²; 0 when absent
	MaxFALL   int               // MaxFALL in cd/m²; 0 when absent
}

// IsPQ reports whether the stream uses the PQ transfer (HDR10 / HDR10+).
func (h *HDRInfo) IsPQ() bool { return h != nil && h.ColorTransfer == transferPQ }

// HasStaticMetadata reports whether mastering-display or content-light data
// is present and worth handing to the encoder.
func (h *HDRInfo) HasStaticMetadata() bool {
	return h != nil && (h.Mastering != nil || h.MaxCLL > 0 || h.MaxFALL > 0)
}

// ProbeHDR reads the first video frame's colour tags and static HDR side data.
// It returns (nil, nil) for SDR sources. Frame-level probing is used because
// HDR10 metadata carried only in HEVC SEI messages does not appear on the
// stream itself.
func ProbeHDR(ctx context.Context, path string) (*HDRInfo, error) {
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-read_intervals", "%+#1",
		"-show_frames",
		"-show_entries", "frame=color_space,color_primaries,color_transfer,color_range:frame_side_data",
		"-of", "json",
		path,
	).Output()
	if err != nil {
		return nil, err
	}
	return parseHDR(out)
}

type hdrFrameJSON struct {
	Frames []struct {
		ColorSpace     string                   `json:"color_space"`
		ColorPrimaries string                   `json:"color_primaries"`
		ColorTransfer  string                   `json:"color_transfer"`
		ColorRange     string                   `json:"color_range"`
		SideData       []map[string]interface{} `json:"side_data_list"`
	} `json:"frames"`
}

// parseHDR decodes ffprobe's JSON frame output. Only PQ and HLG transfers are
// treated as HDR; everything else yields (nil, nil).
func parseHDR(data []byte) (*HDRInfo, error) {
	var doc hdrFrameJSON
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse ffprobe hdr output: %w", err)
	}
	if len(doc.Frames) == 0 {
		return nil, nil
	}
	f := doc.Frames[0]
	if f.ColorTransfer != transferPQ && f.ColorTransfer != transferHLG {
		return nil, nil
	}
	h := &HDRInfo{
		ColorPrimaries: knownTag(f.ColorPrimaries),
		ColorTransfer:  f.ColorTransfer,
		ColorSpace:     knownTag(f.ColorSpace),
		ColorRange:     knownTag(f.ColorRange),
	}
	for _, sd := range f.SideData {
		switch sd["side_data_type"] {
		case "Mastering display metadata":
			h.Mastering = parseMastering(sd)
		case "Content light level metadata":
			h.MaxCLL = jsonInt(sd["max_content"])
			h.MaxFALL = jsonInt(sd["max_average"])
		}
	}
	return h, nil
}

func knownTag(s string) string {
	if s == "unknown" || s == "unspecified" {
		return ""
	}
	return s
}

func parseMastering(sd map[string]interface{}) *MasteringDisplay {
	vals := map[string]float64{}
	for _, k := range []string{
		"red_x", "red_y", "green_x", "green_y", "blue_x", "blue_y",
		"white_point_x", "white_point_y", "min_luminance", "max_luminance",
	} {
		v, ok := parseRational(sd[k])
		if !ok {
			return nil // partial metadata is worse than none
		}
		vals[k] = v
	}
	return &MasteringDisplay{
		RedX: vals["red_x"], RedY: vals["red_y"],
		GreenX: vals["green_x"], GreenY: vals["green_y"],
		BlueX: vals["blue_x"], BlueY: vals["blue_y"],
		WhiteX: vals["white_point_x"], WhiteY: vals["white_point_y"],
		MinLum: vals["min_luminance"], MaxLum: vals["max_luminance"],
	}
}

// parseRational accepts ffprobe's "num/den" strings (or a plain number).
func parseRational(v interface{}) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case string:
		n, d, found := strings.Cut(t, "/")
		num, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return 0, false
		}
		if !found {
			return num, true
		}
		den, err := strconv.ParseFloat(d, 64)
		if err != nil || den == 0 {
			return 0, false
		}
		return num / den, true
	}
	return 0, false
}

func jsonInt(v interface{}) int {
	if f, ok := parseRational(v); ok {
		return int(math.Round(f))
	}
	return 0
}

// x265MasterDisplay formats the x265 --master-display value: chromaticities in
// units of 0.00002 and luminance in units of 0.0001 cd/m².
func (m *MasteringDisplay) x265MasterDisplay() string {
	c := func(v float64) int { return int(math.Round(v * 50000)) }
	l := func(v float64) int { return int(math.Round(v * 10000)) }
	return fmt.Sprintf("G(%d,%d)B(%d,%d)R(%d,%d)WP(%d,%d)L(%d,%d)",
		c(m.GreenX), c(m.GreenY), c(m.BlueX), c(m.BlueY),
		c(m.RedX), c(m.RedY), c(m.WhiteX), c(m.WhiteY),
		l(m.MaxLum), l(m.MinLum))
}

// svtMasterDisplay formats SVT-AV1's mastering-display value, which takes
// decimal chromaticity and luminance rather than x265's fixed-point units.
func (m *MasteringDisplay) svtMasterDisplay() string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	r5 := func(v float64) string { return f(math.Round(v*100000) / 100000) }
	return fmt.Sprintf("G(%s,%s)B(%s,%s)R(%s,%s)WP(%s,%s)L(%s,%s)",
		r5(m.GreenX), r5(m.GreenY), r5(m.BlueX), r5(m.BlueY),
		r5(m.RedX), r5(m.RedY), r5(m.WhiteX), r5(m.WhiteY),
		f(math.Round(m.MaxLum*10000)/10000), f(math.Round(m.MinLum*10000)/10000))
}

// hdrArgs returns the flags that carry a source's HDR signalling through the
// re-encode. The generic colour tags apply to every encoder. Static metadata
// (mastering display, MaxCLL/MaxFALL) is passed explicitly to the software
// encoders that take it; hardware encoders receive it as frame side data
// propagated by ffmpeg. Returns nil for SDR.
func hdrArgs(spec EncodeSpec) []string {
	h := spec.HDR
	if h == nil {
		return nil
	}
	var args []string
	if h.ColorPrimaries != "" {
		args = append(args, "-color_primaries", h.ColorPrimaries)
	}
	if h.ColorTransfer != "" {
		args = append(args, "-color_trc", h.ColorTransfer)
	}
	if h.ColorSpace != "" {
		args = append(args, "-colorspace", h.ColorSpace)
	}
	if h.ColorRange != "" {
		args = append(args, "-color_range", h.ColorRange)
	}
	if spec.Backend != "" || !h.IsPQ() || !h.HasStaticMetadata() {
		return args
	}
	switch videoEncoder(spec) {
	case "libx265":
		p := []string{"hdr10=1", "repeat-headers=1"}
		if h.Mastering != nil {
			p = append(p, "master-display="+h.Mastering.x265MasterDisplay())
		}
		if h.MaxCLL > 0 || h.MaxFALL > 0 {
			p = append(p, fmt.Sprintf("max-cll=%d,%d", h.MaxCLL, h.MaxFALL))
		}
		args = append(args, "-x265-params", strings.Join(p, ":"))
	case "libsvtav1":
		var p []string
		if h.Mastering != nil {
			p = append(p, "mastering-display="+h.Mastering.svtMasterDisplay())
		}
		if h.MaxCLL > 0 || h.MaxFALL > 0 {
			p = append(p, fmt.Sprintf("content-light=%d,%d", h.MaxCLL, h.MaxFALL))
		}
		args = append(args, "-svtav1-params", strings.Join(p, ":"))
	}
	return args
}
