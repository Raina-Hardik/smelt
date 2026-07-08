package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Raina-Hardik/smelt/api"
	"gopkg.in/yaml.v3"
)

// specDoc is the minimal shape of openapi.yaml needed to enumerate routes.
type specDoc struct {
	Paths map[string]map[string]any `yaml:"paths"`
}

func loadSpec(t *testing.T) specDoc {
	t.Helper()
	var doc specDoc
	if err := yaml.Unmarshal(api.SpecYAML, &doc); err != nil {
		t.Fatalf("parse embedded openapi.yaml: %v", err)
	}
	return doc
}

var httpMethods = []string{"get", "post", "put", "delete", "patch", "head", "options"}

// TestSpecRoutesMatchMux guards against the hand-registered routes
// (/openapi.yaml, /docs) or a manual mux edit silently drifting from the
// generated registration: every path+method in openapi.yaml must resolve to
// a registered pattern on the server's mux.
func TestSpecRoutesMatchMux(t *testing.T) {
	e := newTestEnv(t)
	mux, ok := e.h.(*http.ServeMux)
	if !ok {
		t.Fatalf("Handler() did not return *http.ServeMux (got %T)", e.h)
	}

	doc := loadSpec(t)
	if len(doc.Paths) == 0 {
		t.Fatal("embedded spec has no paths")
	}

	for path, ops := range doc.Paths {
		testPath := strings.NewReplacer("{id}", "x").Replace(path)
		for method := range ops {
			upper := strings.ToUpper(method)
			isMethod := false
			for _, m := range httpMethods {
				if method == m {
					isMethod = true
					break
				}
			}
			if !isMethod {
				continue // e.g. a shared "parameters" key
			}
			req := httptest.NewRequest(upper, testPath, nil)
			_, pattern := mux.Handler(req)
			if pattern == "" {
				t.Errorf("%s %s (spec path %s): no route registered on mux", upper, testPath, path)
			}
		}
	}
}

func TestOpenAPISpecServed(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(http.MethodGet, "/openapi.yaml", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/yaml" {
		t.Errorf("Content-Type = %q, want application/yaml", ct)
	}
	if w.Body.String() != string(api.SpecYAML) {
		t.Error("served spec bytes do not match api.SpecYAML")
	}
}
