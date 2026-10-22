package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Raina-Hardik/smelt/internal/ffmpeg"
)

func TestCleanRemovesOnlySmeltChunkDirs(t *testing.T) {
	root := t.TempDir()
	partial := filepath.Join(root, "a.transcoded.mkv")
	ours := filepath.Join(root, "sub", "b.transcoded.mkv.chunks")
	userDir := filepath.Join(root, "sub", "my.transcoded.chunks") // same shape, no marker
	keep := filepath.Join(root, "keep.mkv")
	for _, d := range []string{ours, userDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := []string{
		partial, keep,
		filepath.Join(ours, ffmpeg.ChunkMarker),
		filepath.Join(ours, "enc00000.transcoded.mkv"),
		filepath.Join(userDir, "notes.txt"),
	}
	for _, f := range files {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rootCmd.SetArgs([]string{"clean", "--src", root, "--assume-yes"})
	defer rootCmd.SetArgs(nil)
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	for _, gone := range []string{partial, ours} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should be removed (err=%v)", gone, err)
		}
	}
	for _, kept := range []string{keep, userDir, filepath.Join(userDir, "notes.txt")} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s must be kept (a directory without smelt's marker is never deleted): %v", kept, err)
		}
	}
}
