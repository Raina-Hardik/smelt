//go:build dev

package server

import (
	"net/http"
	"testing"
)

func TestDocsServedInDevBuild(t *testing.T) {
	e := newTestEnv(t)

	w := e.do(http.MethodGet, "/docs", nil)
	if w.Code != http.StatusOK {
		t.Errorf("GET /docs status = %d, want 200", w.Code)
	}

	w = e.do(http.MethodGet, "/docs/scalar.js", nil)
	if w.Code != http.StatusOK {
		t.Errorf("GET /docs/scalar.js status = %d, want 200", w.Code)
	}
	if w.Body.Len() == 0 {
		t.Error("GET /docs/scalar.js returned an empty body")
	}
}
