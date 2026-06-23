//go:build unix

package scanner

import (
	"io/fs"
	"syscall"
)

// fileLinks returns the hardlink count from the underlying stat, or 0 if the
// platform doesn't expose it.
func fileLinks(info fs.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		// Nlink's type varies by platform (uint64 on linux/amd64, uint16 on
		// darwin), so the conversion is needed even where it looks redundant.
		return uint64(st.Nlink) //nolint:unconvert
	}
	return 0
}
