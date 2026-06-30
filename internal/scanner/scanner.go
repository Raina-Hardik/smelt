package scanner

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type MediaFile struct {
	Path    string
	RelPath string // path relative to the scan root; used to mirror the tree into --output-dir
	Ext     string
	Size    int64
	Mtime   int64       // modification time as unix seconds; used for DB-based skip on re-runs
	Mode    os.FileMode // permission bits; applied to the output so perms are preserved
	Links   uint64      // hardlink count (st_nlink); >1 means the file is hardlinked elsewhere
}

// Scan walks root recursively and returns files whose extensions match the
// provided list (case-insensitive, with or without leading dot).
//
// Symlinked directories are not followed, which prevents infinite loops caused
// by circular symlinks (e.g. mise trusted-configs, which symlinks back to $HOME).
// Symlinked files are included — ARR and seedbox setups commonly use them.
// Per-entry errors (permission denied, broken symlinks) are skipped silently;
// only a failure to access root itself is returned.
func Scan(root string, extensions []string) ([]MediaFile, error) {
	extSet := make(map[string]struct{}, len(extensions))
	for _, e := range extensions {
		extSet[strings.ToLower(strings.TrimPrefix(e, "."))] = struct{}{}
	}

	var files []MediaFile
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err // can't access root at all — propagate
			}
			return nil // skip inaccessible subtrees / broken entries
		}

		// filepath.WalkDir does not recurse into symlinked directories, so
		// symlink entries always appear as leaf nodes with ModeSymlink set.
		// For directory symlinks we simply skip; for file symlinks we follow
		// via os.Stat to get the real file metadata.
		if d.Type()&fs.ModeSymlink != 0 {
			fi, statErr := os.Stat(path)
			if statErr != nil || fi.IsDir() {
				return nil // broken or directory symlink — skip
			}
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
			if _, ok := extSet[ext]; !ok {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			files = append(files, MediaFile{
				Path:    path,
				RelPath: rel,
				Ext:     ext,
				Size:    fi.Size(),
				Mtime:   fi.ModTime().Unix(),
				Mode:    fi.Mode(),
				Links:   fileLinks(fi),
			})
			return nil
		}

		if d.IsDir() {
			return nil
		}

		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
		if _, ok := extSet[ext]; !ok {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil // file disappeared between walk and stat
		}
		rel, _ := filepath.Rel(root, path)
		files = append(files, MediaFile{
			Path:    path,
			RelPath: rel,
			Ext:     ext,
			Size:    info.Size(),
			Mtime:   info.ModTime().Unix(),
			Mode:    info.Mode(),
			Links:   fileLinks(info),
		})
		return nil
	})
	return files, err
}
