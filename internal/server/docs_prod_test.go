//go:build !dev

package server

import (
	"net/http"
	"testing"
)

func TestDocsNotServedInProdBuild(t *testing.T) {
	e := newTestEnv(t)

	w := e.do(http.MethodGet, "/docs", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("GET /docs status = %d, want 404 (prod build must not mount Scalar)", w.Code)
	}
}
