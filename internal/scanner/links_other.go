//go:build !unix

package scanner

import "io/fs"

// fileLinks is a no-op on platforms that don't expose a hardlink count via
// stat; --skip-hardlinked then has nothing to act on.
func fileLinks(info fs.FileInfo) uint64 { return 0 }
