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
