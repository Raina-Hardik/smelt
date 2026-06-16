package scanner

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

type MediaFile struct {
	Path string
	Ext  string
	Size int64
}

// Scan walks root recursively and returns files whose extensions match the
// provided list (case-insensitive, with or without leading dot).
func Scan(root string, extensions []string) ([]MediaFile, error) {
	extSet := make(map[string]struct{}, len(extensions))
	for _, e := range extensions {
		extSet[strings.ToLower(strings.TrimPrefix(e, "."))] = struct{}{}
	}

	fsys := os.DirFS(root)

	var files []MediaFile
	err := doublestar.GlobWalk(fsys, "**", func(path string, d os.DirEntry) error {
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
		if _, ok := extSet[ext]; !ok {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files = append(files, MediaFile{
			Path: filepath.Join(root, path),
			Ext:  ext,
			Size: info.Size(),
		})
		return nil
	})
	return files, err
}
