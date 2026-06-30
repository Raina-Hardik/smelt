package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScan(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.mkv"))
	mustWrite(t, filepath.Join(root, "sub", "b.MP4")) // nested + uppercase ext
	mustWrite(t, filepath.Join(root, "sub", "c.txt")) // excluded extension
	mustWrite(t, filepath.Join(root, "d.mp4"))

	// leading dot on one extension should be tolerated
	files, err := Scan(root, []string{"mkv", ".mp4"})
	if err != nil {
		t.Fatal(err)
	}

	byRel := make(map[string]MediaFile, len(files))
	for _, f := range files {
		byRel[f.RelPath] = f
	}

	if len(byRel) != 3 {
		t.Fatalf("expected 3 matches, got %d: %v", len(byRel), byRel)
	}
	if _, ok := byRel["c.txt"]; ok {
		t.Error("c.txt should have been excluded")
	}

	nested := filepath.Join("sub", "b.MP4")
	f, ok := byRel[nested]
	if !ok {
		t.Fatalf("missing nested match %q in %v", nested, byRel)
	}
	if want := filepath.Join(root, "sub", "b.MP4"); f.Path != want {
		t.Errorf("Path = %q, want %q", f.Path, want)
	}
	if f.Ext != "mp4" {
		t.Errorf("Ext = %q, want %q (lowercased)", f.Ext, "mp4")
	}
}

func TestScanReportsHardlinks(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a.mkv")
	mustWrite(t, a)
	if err := os.Link(a, filepath.Join(root, "b.mkv")); err != nil {
		t.Skipf("hardlinks unsupported here: %v", err)
	}
	files, err := Scan(root, []string{"mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("scanned %d files, want 2", len(files))
	}
	for _, f := range files {
		if f.Links == 0 {
			t.Skip("platform does not report link counts")
		}
		if f.Links != 2 {
			t.Errorf("%s: Links = %d, want 2", f.Path, f.Links)
		}
	}
}

// TestScanSkipsCircularSymlink verifies that a directory symlink pointing back
// to an ancestor does not cause an infinite walk or an ELOOP error. This
// reproduces the mise trusted-configs circular-symlink issue observed on Linux.
func TestScanSkipsCircularSymlink(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.mkv"))
	// Create a symlink inside root that points back to root — circular.
	if err := os.Symlink(root, filepath.Join(root, "loop")); err != nil {
		t.Skipf("symlinks not supported here: %v", err)
	}
	files, err := Scan(root, []string{"mkv"})
	if err != nil {
		t.Fatalf("Scan should not fail on circular symlinks, got: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d: %v", len(files), files)
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}
