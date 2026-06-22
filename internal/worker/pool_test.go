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
		src  string
		cfg  *config.Config
		want string
	}{
		{"default smelt suffix", "/media/a.mkv", &config.Config{Suffix: ".smelt"}, "/media/a.smelt.mkv"},
		{"inplace returns original", "/media/a.mkv", &config.Config{InPlace: true, Suffix: ".smelt"}, "/media/a.mkv"},
		{"custom suffix", "/media/a.mp4", &config.Config{Suffix: ".enc"}, "/media/a.enc.mp4"},
		{"no extension", "/media/a", &config.Config{Suffix: ".smelt"}, "/media/a.smelt"},
	}
	for _, c := range cases {
		if got := OutputPath(c.src, c.cfg); got != c.want {
			t.Errorf("%s: OutputPath(%q) = %q, want %q", c.name, c.src, got, c.want)
		}
	}
}

func TestTransientPath(t *testing.T) {
	if got := transientPath("/media/a.mkv"); got != "/media/a.transcoded.mkv" {
		t.Errorf("transientPath = %q, want /media/a.transcoded.mkv", got)
	}
}

// A failed encode must leave no partial artifact behind. ffmpeg is invoked on a
// non-existent source so Run fails fast without needing real media.
func TestTranscodeCleansUpOnFailure(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "missing.mkv")

	p := New(&config.Config{Workers: 1, Codec: "h264", Suffix: ".smelt"})
	err := p.transcode(context.Background(), scanner.MediaFile{Path: src}, nil)
	if err == nil {
		t.Fatal("expected an error transcoding a non-existent source")
	}
	if _, statErr := os.Stat(transientPath(src)); !os.IsNotExist(statErr) {
		t.Errorf("transient artifact %q should have been removed", transientPath(src))
	}
}
