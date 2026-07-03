package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/scanner"
)

func p(s string) string { return filepath.FromSlash(s) }

func TestResourceProfileWarnsOnlyForHardwareBackend(t *testing.T) {
	if got := BuildResourceProfile("libx265", "", 0); got.Warn {
		t.Error("software backend should not warn")
	}
	if got := BuildResourceProfile("hevc_nvenc", "nvenc", 0); !got.Warn {
		t.Error("hardware backend should warn (software decode + hardware encode)")
	}
}

func TestResourceProfileDecodeLabel(t *testing.T) {
	cases := []struct {
		threads int
		want    string
	}{
		{0, "software (uncapped)"},
		{1, "software (capped at 1 thread)"},
		{4, "software (capped at 4 threads)"},
	}
	for _, c := range cases {
		got := BuildResourceProfile("libx265", "", c.threads).DecodeLabel()
		if got != c.want {
			t.Errorf("DecodeLabel(%d) = %q, want %q", c.threads, got, c.want)
		}
	}
}

func TestOutputPath(t *testing.T) {
	cases := []struct {
		name string
		file scanner.MediaFile
		cfg  *config.Config
		want string
	}{
		{"default smelt suffix", scanner.MediaFile{Path: p("/media/a.mkv")}, &config.Config{Suffix: ".smelt"}, p("/media/a.smelt.mkv")},
		{"inplace returns original", scanner.MediaFile{Path: p("/media/a.mkv")}, &config.Config{InPlace: true, Suffix: ".smelt"}, p("/media/a.mkv")},
		{"custom suffix", scanner.MediaFile{Path: p("/media/a.mp4")}, &config.Config{Suffix: ".enc"}, p("/media/a.enc.mp4")},
		{"no extension", scanner.MediaFile{Path: p("/media/a")}, &config.Config{Suffix: ".smelt"}, p("/media/a.smelt")},
		{"output-dir mirrors rel tree, keeps name",
			scanner.MediaFile{Path: p("/media/movies/a.mkv"), RelPath: p("movies/a.mkv")},
			&config.Config{Suffix: ".smelt", OutputDir: p("/out")}, p("/out/movies/a.mkv")},
		{"--to swaps container ext",
			scanner.MediaFile{Path: p("/media/a.mkv")},
			&config.Config{Suffix: ".smelt", Container: "mp4"}, p("/media/a.smelt.mp4")},
		{"--to with output-dir swaps ext in mirrored tree",
			scanner.MediaFile{Path: p("/media/movies/a.mkv"), RelPath: p("movies/a.mkv")},
			&config.Config{Suffix: ".smelt", OutputDir: p("/out"), Container: "mp4"}, p("/out/movies/a.mp4")},
	}
	for _, c := range cases {
		if got := OutputPath(c.file, c.cfg); got != c.want {
			t.Errorf("%s: OutputPath = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTransientPath(t *testing.T) {
	// alongside-source default: transient sits next to the final file
	want := filepath.FromSlash("/media/a.transcoded.mkv")
	if got := transientPath(filepath.FromSlash("/media/a.smelt.mkv"), filepath.FromSlash("/media/a.mkv")); got != want {
		t.Errorf("transientPath = %q, want %q", got, want)
	}
	// output-dir: transient lives in the destination dir, not the source dir
	want = filepath.FromSlash("/out/movies/a.transcoded.mkv")
	if got := transientPath(filepath.FromSlash("/out/movies/a.mkv"), filepath.FromSlash("/media/movies/a.mkv")); got != want {
		t.Errorf("transientPath = %q, want %q", got, want)
	}
}

func TestPlanExcludesOwnOutputsAndExisting(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.mkv")
	own := filepath.Join(dir, "b.smelt.mkv") // a previously generated output
	fresh := filepath.Join(dir, "c.mkv")
	for _, p := range []string{src, fresh, own, filepath.Join(dir, "a.smelt.mkv")} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.Config{Suffix: ".smelt"}
	files := []scanner.MediaFile{{Path: src}, {Path: own}, {Path: fresh}}
	todo, skipped, _ := Plan(context.Background(), files, cfg, nil)

	// a.mkv skipped (a.smelt.mkv exists), b.smelt.mkv skipped (own output), c.mkv kept.
	if skipped != 2 || len(todo) != 1 || todo[0].Path != fresh {
		t.Fatalf("Plan = (todo %v, skipped %d), want ([c.mkv], 2)", todo, skipped)
	}
}

// AllowHDRLoss must short-circuit the DV probe entirely — no ffprobe call,
// no blocked count — since a file that fails ffprobe (e.g. this test's
// plain-text stand-in) would otherwise pass through anyway.
func TestPlanAllowHDRLossSkipsDVCheck(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Suffix: ".smelt", AllowHDRLoss: true}
	todo, skipped, blocked := Plan(context.Background(), []scanner.MediaFile{{Path: src}}, cfg, nil)
	if skipped != 0 || blocked != 0 || len(todo) != 1 {
		t.Fatalf("Plan = (todo %d, skipped %d, blocked %d), want (1, 0, 0)", len(todo), skipped, blocked)
	}
}

func TestPlanForceKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.mkv")
	if err := os.WriteFile(filepath.Join(dir, "a.smelt.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	todo, skipped, _ := Plan(context.Background(), []scanner.MediaFile{{Path: src}}, &config.Config{Suffix: ".smelt", Force: true}, nil)
	if skipped != 0 || len(todo) != 1 {
		t.Errorf("with --force: (todo %d, skipped %d), want (1, 0)", len(todo), skipped)
	}
}

// In --inplace mode, files already in the target codec are skipped; others kept.
func TestPlanInplaceSmartSkip(t *testing.T) {
	cfg := &config.Config{Codec: "h265", InPlace: true} // target codec_name "hevc"
	files := []scanner.MediaFile{{Path: "/x/already.mkv"}, {Path: "/x/old.mkv"}, {Path: "/x/broken.mkv"}}
	probe := func(p string) (string, error) {
		switch p {
		case "/x/already.mkv":
			return "hevc", nil // matches target → skip
		case "/x/old.mkv":
			return "h264", nil // different → keep
		default:
			return "", errors.New("probe failed") // unknown → keep
		}
	}
	todo, skipped := planInplace(files, cfg, nil, probe)
	if skipped != 1 || len(todo) != 2 {
		t.Fatalf("smart-skip: (todo %d, skipped %d), want (2, 1)", len(todo), skipped)
	}
	if todo[0].Path != "/x/old.mkv" || todo[1].Path != "/x/broken.mkv" {
		t.Errorf("kept the wrong files: %v", todo)
	}
}

// --skip-hardlinked drops files hardlinked elsewhere (regardless of codec);
// single-link files still go through the normal codec check.
func TestPlanInplaceSkipHardlinked(t *testing.T) {
	cfg := &config.Config{Codec: "h265", InPlace: true, SkipHardlinked: true}
	files := []scanner.MediaFile{
		{Path: "/x/linked.mkv", Links: 2}, // hardlinked → skip even though h264
		{Path: "/x/solo.mkv", Links: 1},   // single link, h264 → keep
	}
	probe := func(string) (string, error) { return "h264", nil }
	todo, skipped := planInplace(files, cfg, nil, probe)
	if skipped != 1 || len(todo) != 1 || todo[0].Path != "/x/solo.mkv" {
		t.Fatalf("skip-hardlinked: (todo %v, skipped %d), want ([solo], 1)", todo, skipped)
	}
}

// Without the flag, a hardlinked file is transcoded normally.
func TestPlanInplaceHardlinkedKeptByDefault(t *testing.T) {
	cfg := &config.Config{Codec: "h265", InPlace: true}
	files := []scanner.MediaFile{{Path: "/x/linked.mkv", Links: 5}}
	todo, skipped := planInplace(files, cfg, nil, func(string) (string, error) { return "h264", nil })
	if skipped != 0 || len(todo) != 1 {
		t.Errorf("default: (todo %d, skipped %d), want (1, 0)", len(todo), skipped)
	}
}

// --force overrides smart-skip and keeps everything, without probing.
func TestPlanInplaceForceKeepsAll(t *testing.T) {
	cfg := &config.Config{Codec: "h265", InPlace: true, Force: true}
	files := []scanner.MediaFile{{Path: "/x/a.mkv"}, {Path: "/x/b.mkv"}}
	probed := false
	todo, skipped := planInplace(files, cfg, nil, func(string) (string, error) { probed = true; return "hevc", nil })
	if skipped != 0 || len(todo) != 2 || probed {
		t.Errorf("force: (todo %d, skipped %d, probed %v), want (2, 0, false)", len(todo), skipped, probed)
	}
}

// A failed encode must leave no partial artifact behind. ffmpeg is invoked on a
// non-existent source so Run fails fast without needing real media.
func TestTranscodeCleansUpOnFailure(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "missing.mkv")
	cfg := &config.Config{Workers: 1, Codec: "h264", Suffix: ".smelt"}

	p := New(cfg, nil)
	f := scanner.MediaFile{Path: src}
	err := p.transcode(context.Background(), f, nil)
	if err == nil {
		t.Fatal("expected an error transcoding a non-existent source")
	}
	tmp := transientPath(OutputPath(f, cfg), src)
	if _, statErr := os.Stat(tmp); !os.IsNotExist(statErr) {
		t.Errorf("transient artifact %q should have been removed", tmp)
	}
}
