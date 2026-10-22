package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/ffmpeg"
	"github.com/Raina-Hardik/smelt/internal/scanner"
)

func p(s string) string { return filepath.FromSlash(s) }

func TestResourceProfileWarnsOnlyForSoftwareDecodeWithHardwareEncode(t *testing.T) {
	if got := BuildResourceProfile("libx265", "", 0, "auto"); got.Warn {
		t.Error("software backend should not warn")
	}
	if got := BuildResourceProfile("hevc_nvenc", "nvenc", 0, "off"); !got.Warn {
		t.Error("hwdecode off + hardware encode should warn (software decode concurrent with hw encode)")
	}
	full := BuildResourceProfile("hevc_nvenc", "nvenc", 0, "auto")
	if full.Warn || !full.DecodeHW {
		t.Errorf("hwdecode auto on nvenc = %+v, want DecodeHW=true and no warning (full hardware pipeline)", full)
	}
	if got := BuildResourceProfile("hevc_amf", "amf", 0, "auto"); !got.Warn || got.DecodeHW {
		t.Errorf("amf is encode-only: got %+v, want software decode + warning", got)
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
		got := BuildResourceProfile("libx265", "", c.threads, "auto").DecodeLabel()
		if got != c.want {
			t.Errorf("DecodeLabel(%d) = %q, want %q", c.threads, got, c.want)
		}
	}
}

func TestResourceProfileGovernorHint(t *testing.T) {
	origLookPath := lookPath
	defer func() { lookPath = origLookPath }()

	t.Run("without systemd-run", func(t *testing.T) {
		lookPath = func(string) (string, error) { return "", errors.New("not found") }
		got := BuildResourceProfile("hevc_nvenc", "nvenc", 4, "off").GovernorHint()
		want := "remove --hwdecode off; or lower --decode-threads (try 4) and/or --workers"
		if got != want {
			t.Errorf("GovernorHint() = %q, want %q", got, want)
		}
		if strings.Contains(got, "systemd-run") {
			t.Errorf("GovernorHint() = %q, should not suggest systemd-run when absent", got)
		}
	})

	t.Run("with systemd-run on linux", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("systemd-run branch only applies on linux")
		}
		lookPath = func(string) (string, error) { return "/usr/bin/systemd-run", nil }
		got := BuildResourceProfile("hevc_nvenc", "nvenc", 0, "off").GovernorHint()
		want := "remove --hwdecode off; or lower --decode-threads (try 2) and/or --workers; or wrap the run: systemd-run --scope -p CPUQuota=50% --nice=10 smelt transcode --decode-threads 2 --workers 1 ..."
		if got != want {
			t.Errorf("GovernorHint() = %q, want %q", got, want)
		}
	})
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

// Two sources that map to the same output (a.mkv + a.mp4 with --to mkv) would
// be encoded concurrently into one file; only the first may run.
func TestPlanSkipsSourcesWithCollidingOutputs(t *testing.T) {
	dir := t.TempDir()
	files := []scanner.MediaFile{
		{Path: filepath.Join(dir, "a.mkv"), RelPath: "a.mkv"},
		{Path: filepath.Join(dir, "a.mp4"), RelPath: "a.mp4"},
		{Path: filepath.Join(dir, "b.mkv"), RelPath: "b.mkv"},
	}
	todo, skipped, _ := Plan(context.Background(), files, &config.Config{Suffix: ".smelt", Container: "mkv", AllowHDRLoss: true}, nil)
	if len(todo) != 2 || skipped != 1 || todo[0].Path != files[0].Path || todo[1].Path != files[2].Path {
		t.Fatalf("Plan = (todo %v, skipped %d), want a.mkv and b.mkv with a.mp4 skipped", todo, skipped)
	}

	// Without --to the outputs differ (a.smelt.mkv / a.smelt.mp4): both run.
	todo, skipped, _ = Plan(context.Background(), files[:2], &config.Config{Suffix: ".smelt", AllowHDRLoss: true}, nil)
	if len(todo) != 2 || skipped != 0 {
		t.Fatalf("distinct outputs must both run: todo %d skipped %d", len(todo), skipped)
	}
}

// In --inplace mode smelt's own transient files and chunk work files are never
// sources: replacing them in place would corrupt an interrupted run's state.
func TestPlanInplaceIgnoresOwnTransientFiles(t *testing.T) {
	files := []scanner.MediaFile{
		{Path: "/m/a.mkv"},
		{Path: "/m/a.transcoded.mkv"},
		{Path: "/m/b.mkv.transcoded.mkv.chunks/enc00000.transcoded.mkv"},
	}
	todo, skipped, _ := Plan(context.Background(), files, &config.Config{Codec: "h265", InPlace: true, AllowHDRLoss: true, Force: true}, nil)
	if len(todo) != 1 || todo[0].Path != "/m/a.mkv" || skipped != 2 {
		t.Fatalf("Plan = (todo %v, skipped %d), want only a.mkv with 2 skipped", todo, skipped)
	}
}

func TestVerifyOutput(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "out.mkv")
	if err := os.WriteFile(good, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.mkv")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	orig := probeHealth
	t.Cleanup(func() { probeHealth = orig })

	health := func(src, out *ffmpeg.FileHealth, outErr error) {
		probeHealth = func(_ context.Context, path string) (*ffmpeg.FileHealth, error) {
			if path == good {
				return out, outErr
			}
			return src, nil
		}
	}
	min := func(n int) time.Duration { return time.Duration(n) * time.Minute }

	cases := []struct {
		name    string
		out     string
		src     *ffmpeg.FileHealth
		dst     *ffmpeg.FileHealth
		dstErr  error
		wantErr bool
	}{
		{"matching duration", good, &ffmpeg.FileHealth{HasVideo: true, Duration: min(90)}, &ffmpeg.FileHealth{HasVideo: true, Duration: min(90)}, nil, false},
		{"within tolerance", good, &ffmpeg.FileHealth{HasVideo: true, Duration: min(90)}, &ffmpeg.FileHealth{HasVideo: true, Duration: min(90) + time.Second}, nil, false},
		{"truncated output", good, &ffmpeg.FileHealth{HasVideo: true, Duration: min(90)}, &ffmpeg.FileHealth{HasVideo: true, Duration: min(30)}, nil, true},
		{"no video stream", good, &ffmpeg.FileHealth{HasVideo: true, Duration: min(90)}, &ffmpeg.FileHealth{HasVideo: false, Duration: min(90)}, nil, true},
		{"unreadable output", good, &ffmpeg.FileHealth{HasVideo: true, Duration: min(90)}, nil, errors.New("invalid data"), true},
		{"empty file", empty, nil, nil, nil, true},
		{"missing file", filepath.Join(dir, "nope.mkv"), nil, nil, nil, true},
		{"source duration unknown", good, &ffmpeg.FileHealth{HasVideo: true}, &ffmpeg.FileHealth{HasVideo: true, Duration: min(5)}, nil, false},
	}
	for _, c := range cases {
		health(c.src, c.dst, c.dstErr)
		err := verifyOutput(context.Background(), "/m/src.mkv", c.out)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", c.name, err, c.wantErr)
		}
	}
}

// A symlink must never be replaced in place: the link would become a regular
// file and its target would be encoded as a second copy.
func TestPlanInplaceSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.mkv")
	link := filepath.Join(dir, "link.mkv")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	files := []scanner.MediaFile{{Path: real}, {Path: link}}
	todo, skipped, _ := Plan(context.Background(), files, &config.Config{Codec: "h265", InPlace: true, Force: true, AllowHDRLoss: true}, nil)
	if len(todo) != 1 || todo[0].Path != real || skipped != 1 {
		t.Fatalf("Plan = (todo %v, skipped %d), want only the real file", todo, skipped)
	}

	// Non-inplace runs write a new file and are unaffected.
	todo, _, _ = Plan(context.Background(), files, &config.Config{Suffix: ".smelt", AllowHDRLoss: true}, nil)
	if len(todo) != 2 {
		t.Errorf("non-inplace must keep symlinked sources: %v", todo)
	}
}
