//go:build dev

package server

import (
	_ "embed"
	"net/http"
)

// scalarStandaloneJS is the @scalar/api-reference standalone browser bundle,
// pinned at v1.62.5 (https://www.jsdelivr.com/package/npm/@scalar/api-reference).
// Refresh with `just vendor-scalar`.
//
//go:embed scalar.standalone.min.js
var scalarStandaloneJS []byte

const docsHTML = `<!doctype html>
<html>
<head>
  <title>smelt API docs</title>
  <meta charset="utf-8" />
</head>
<body>
  <script id="api-reference" data-url="/openapi.yaml" src="/docs/scalar.js"></script>
</body>
</html>`

// registerDocs mounts the dev-only Scalar API reference UI at GET /docs,
// backed by the vendored standalone bundle at GET /docs/scalar.js. Compiled
// out of non-dev builds entirely (see docs_prod.go).
func registerDocs(mux *http.ServeMux) {
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(docsHTML))
	})
	mux.HandleFunc("GET /docs/scalar.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = w.Write(scalarStandaloneJS)
	})
}
