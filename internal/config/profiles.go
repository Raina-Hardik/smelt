package config

import (
	"sort"

	"github.com/spf13/viper"
)

// Profile is a named, composable preset for a transcode run. It is nothing
// more than a preconfigured set of `smelt transcode` flags — every field maps
// to a CLI flag, and `--profile <name>` expands to exactly that flag set
// (explicit flags on the command line still win). Any field left at its zero
// value is "unset" and does not override the built-in default or an explicit
// flag. This keeps profiles inside the Help-Driven-Development contract: a
// profile can never introduce behaviour the help screen can't describe.
//
// CRF is a *int rather than an int so a profile can distinguish "I don't set
// crf" (nil) from "I set crf 0" (lossless), mirroring viper.IsSet semantics.
type Profile struct {
	Name         string
	Description  string
	Codec        string   // --codec
	CRF          *int     // --crf
	Preset       string   // --preset
	AudioCodec   string   // --audio-codec
	AudioBitrate string   // --audio-bitrate
	Container    string   // --to
	Subs         string   // --subs
	ExtraArgs    []string // --ffmpeg-arg (repeatable), prepended before CLI --ffmpeg-arg
}

func crf(n int) *int { return &n }

// builtinProfiles are the profiles baked into the binary, so `--profile web`
// works with no config file at all. A user-defined profile of the same name in
// config.yaml overrides the built-in field-by-field (see ResolveProfile).
//
// Each is a curated flag set for a common target; nothing here is expressible
// only through a profile — every value has an equivalent explicit flag.
var builtinProfiles = map[string]Profile{
	"web": {
		Name:         "web",
		Description:  "H.264 MP4 for broad browser/device compatibility (faststart).",
		Codec:        "h264",
		CRF:          crf(23),
		Preset:       "fast",
		AudioCodec:   "aac",
		AudioBitrate: "160k",
		Container:    "mp4",
		ExtraArgs:    []string{"-movflags", "+faststart"},
	},
	"web-hevc": {
		Name:         "web-hevc",
		Description:  "H.265 MP4, ~half the size of web; hvc1 tag for Apple/Safari playback.",
		Codec:        "h265",
		CRF:          crf(26),
		Preset:       "medium",
		AudioCodec:   "aac",
		AudioBitrate: "160k",
		Container:    "mp4",
		ExtraArgs:    []string{"-tag:v", "hvc1", "-movflags", "+faststart"},
	},
	"archive": {
		Name:        "archive",
		Description: "High-quality H.265 for long-term storage; audio kept untouched.",
		Codec:       "h265",
		CRF:         crf(18),
		Preset:      "slow",
		AudioCodec:  "copy",
	},
	"av1": {
		Name:         "av1",
		Description:  "AV1 for maximum compression; slow encode, smallest files.",
		Codec:        "av1",
		CRF:          crf(30),
		Preset:       "slow",
		AudioCodec:   "opus",
		AudioBitrate: "128k",
	},
	"mobile": {
		Name:         "mobile",
		Description:  "720p H.264 MP4 for phones/tablets; downscales, small files.",
		Codec:        "h264",
		CRF:          crf(26),
		Preset:       "fast",
		AudioCodec:   "aac",
		AudioBitrate: "96k",
		Container:    "mp4",
		ExtraArgs:    []string{"-vf", "scale=-2:720", "-movflags", "+faststart"},
	},
}

// BuiltinProfile returns the built-in profile of the given name, if any.
func BuiltinProfile(name string) (Profile, bool) {
	p, ok := builtinProfiles[name]
	return p, ok
}

// HasConfigProfile reports whether name is defined under the `profiles`
// section of the loaded config.yaml (independent of any built-in of that name).
func HasConfigProfile(name string) bool {
	return viper.IsSet("profiles." + name)
}

// IsKnownProfile reports whether name resolves to a built-in profile or one
// defined under the `profiles` section of the loaded config.
func IsKnownProfile(name string) bool {
	if _, ok := builtinProfiles[name]; ok {
		return true
	}
	return viper.IsSet("profiles." + name)
}

// ProfileNames returns every known profile name (built-in ∪ config), sorted.
func ProfileNames() []string {
	seen := map[string]struct{}{}
	for n := range builtinProfiles {
		seen[n] = struct{}{}
	}
	if m, ok := viper.Get("profiles").(map[string]any); ok {
		for n := range m {
			seen[n] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// BuiltinProfileNames returns the built-in profile names, sorted. Used by the
// `smelt profiles` listing to mark which entries ship in the binary.
func BuiltinProfileNames() []string {
	names := make([]string, 0, len(builtinProfiles))
	for n := range builtinProfiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ResolveProfile returns the effective profile for name: the built-in (if any)
// with every field the user set under `profiles.<name>` in config.yaml layered
// on top. A user config.yaml therefore *customises* a built-in of the same name
// rather than being shadowed by it. Returns ok=false only if name is unknown to
// both sources. extra_args, when set in config, replaces the built-in's list
// wholesale (it is not appended) so the user has full control.
func ResolveProfile(name string) (Profile, bool) {
	if name == "" {
		return Profile{}, false
	}
	p, isBuiltin := builtinProfiles[name]
	p.Name = name
	base := "profiles." + name
	hasConfig := viper.IsSet(base)
	if !isBuiltin && !hasConfig {
		return Profile{}, false
	}
	if hasConfig {
		if v := viper.GetString(base + ".description"); v != "" {
			p.Description = v
		}
		if v := viper.GetString(base + ".codec"); v != "" {
			p.Codec = v
		}
		if viper.IsSet(base + ".crf") {
			p.CRF = crf(viper.GetInt(base + ".crf"))
		}
		if v := viper.GetString(base + ".preset"); v != "" {
			p.Preset = v
		}
		if v := viper.GetString(base + ".audio_codec"); v != "" {
			p.AudioCodec = v
		}
		if v := viper.GetString(base + ".audio_bitrate"); v != "" {
			p.AudioBitrate = v
		}
		if v := viper.GetString(base + ".to"); v != "" {
			p.Container = v
		}
		if v := viper.GetString(base + ".subs"); v != "" {
			p.Subs = v
		}
		if viper.IsSet(base + ".extra_args") {
			p.ExtraArgs = viper.GetStringSlice(base + ".extra_args")
		}
	}
	return p, true
}
