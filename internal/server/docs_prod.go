//go:build !dev

package server

import "net/http"

// registerDocs is a no-op in non-dev builds: the Scalar docs UI and its JS
// bundle are compiled out entirely, so prod binaries carry zero extra bytes
// and zero extra routes.
func registerDocs(_ *http.ServeMux) {}
