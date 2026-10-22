package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanRemovesChunkWorkDirs(t *testing.T) {
	root := t.TempDir()
	partial := filepath.Join(root, "a.transcoded.mkv")
	chunkDir := filepath.Join(root, "sub", "b.transcoded.chunks")
	keep := filepath.Join(root, "keep.mkv")
	keepDir := filepath.Join(root, "sub", "plain")
	for _, d := range []string{chunkDir, keepDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{partial, keep, filepath.Join(chunkDir, "seg00000.transcoded.mkv"), filepath.Join(keepDir, "x.mkv")} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rootCmd.SetArgs([]string{"clean", "--src", root, "--assume-yes"})
	defer rootCmd.SetArgs(nil)
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}

	for _, gone := range []string{partial, chunkDir} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should be removed (err=%v)", gone, err)
		}
	}
	for _, kept := range []string{keep, keepDir, filepath.Join(keepDir, "x.mkv")} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s should be kept: %v", kept, err)
		}
	}
}
