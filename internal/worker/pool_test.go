package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/scanner"
)

func TestOutputPath(t *testing.T) {
	cases := []struct {
		name string
		file scanner.MediaFile
		cfg  *config.Config
		want string
	}{
		{"default smelt suffix", scanner.MediaFile{Path: "/media/a.mkv"}, &config.Config{Suffix: ".smelt"}, "/media/a.smelt.mkv"},
		{"inplace returns original", scanner.MediaFile{Path: "/media/a.mkv"}, &config.Config{InPlace: true, Suffix: ".smelt"}, "/media/a.mkv"},
		{"custom suffix", scanner.MediaFile{Path: "/media/a.mp4"}, &config.Config{Suffix: ".enc"}, "/media/a.enc.mp4"},
		{"no extension", scanner.MediaFile{Path: "/media/a"}, &config.Config{Suffix: ".smelt"}, "/media/a.smelt"},
		{"output-dir mirrors rel tree, keeps name",
			scanner.MediaFile{Path: "/media/movies/a.mkv", RelPath: "movies/a.mkv"},
			&config.Config{Suffix: ".smelt", OutputDir: "/out"}, "/out/movies/a.mkv"},
	}
	for _, c := range cases {
		if got := OutputPath(c.file, c.cfg); got != c.want {
			t.Errorf("%s: OutputPath = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTransientPath(t *testing.T) {
	// alongside-source default: transient sits next to the final file
	if got := transientPath("/media/a.smelt.mkv", "/media/a.mkv"); got != "/media/a.transcoded.mkv" {
		t.Errorf("transientPath = %q, want /media/a.transcoded.mkv", got)
	}
	// output-dir: transient lives in the destination dir, not the source dir
	if got := transientPath("/out/movies/a.mkv", "/media/movies/a.mkv"); got != "/out/movies/a.transcoded.mkv" {
		t.Errorf("transientPath = %q, want /out/movies/a.transcoded.mkv", got)
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
	todo, skipped := Plan(files, cfg)

	// a.mkv skipped (a.smelt.mkv exists), b.smelt.mkv skipped (own output), c.mkv kept.
	if skipped != 2 || len(todo) != 1 || todo[0].Path != fresh {
		t.Fatalf("Plan = (todo %v, skipped %d), want ([c.mkv], 2)", todo, skipped)
	}
}

func TestPlanForceKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.mkv")
	if err := os.WriteFile(filepath.Join(dir, "a.smelt.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	todo, skipped := Plan([]scanner.MediaFile{{Path: src}}, &config.Config{Suffix: ".smelt", Force: true})
	if skipped != 0 || len(todo) != 1 {
		t.Errorf("with --force: (todo %d, skipped %d), want (1, 0)", len(todo), skipped)
	}
}

func TestPlanInplacePassesThrough(t *testing.T) {
	files := []scanner.MediaFile{{Path: "/x/a.mkv"}, {Path: "/x/b.smelt.mkv"}}
	todo, skipped := Plan(files, &config.Config{Suffix: ".smelt", InPlace: true})
	if skipped != 0 || len(todo) != 2 {
		t.Errorf("inplace passthrough: (todo %d, skipped %d), want (2, 0)", len(todo), skipped)
	}
}

// A failed encode must leave no partial artifact behind. ffmpeg is invoked on a
// non-existent source so Run fails fast without needing real media.
func TestTranscodeCleansUpOnFailure(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "missing.mkv")
	cfg := &config.Config{Workers: 1, Codec: "h264", Suffix: ".smelt"}

	p := New(cfg)
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
