package cmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Raina-Hardik/smelt/api"
	"gopkg.in/yaml.v3"
)

// specDoc mirrors the minimal shape of openapi.yaml needed to enumerate routes.
type specDoc struct {
	Paths map[string]map[string]any `yaml:"paths"`
}

var httpMethods = map[string]bool{
	"get": true, "post": true, "put": true, "delete": true, "patch": true,
}

// routesFromSpec returns the {METHOD, path} set the embedded openapi.yaml declares.
func routesFromSpec(t *testing.T) map[string]bool {
	t.Helper()
	var doc specDoc
	if err := yaml.Unmarshal(api.SpecYAML, &doc); err != nil {
		t.Fatalf("parse embedded openapi.yaml: %v", err)
	}
	routes := make(map[string]bool)
	for path, ops := range doc.Paths {
		for method := range ops {
			if httpMethods[method] {
				routes[strings.ToUpper(method)+" "+path] = true
			}
		}
	}
	return routes
}

var apiRouteLine = regexp.MustCompile(`(?m)^\s*(GET|POST|PUT|DELETE)\s+(\S+)`)

// routesFromHelp extracts the /api/* route lines documented in serveCmd.Long
// (the same text docs/CLI.md mirrors), ignoring the /openapi.yaml and /docs
// lines which aren't part of the generated API contract.
func routesFromHelp() map[string]bool {
	routes := make(map[string]bool)
	for _, m := range apiRouteLine.FindAllStringSubmatch(serveCmd.Long, -1) {
		method, path := m[1], m[2]
		if !strings.HasPrefix(path, "/api/") {
			continue
		}
		routes[method+" "+path] = true
	}
	return routes
}

// TestServeHelpMatchesOpenAPISpec guards HDD lockstep: the API route list in
// `smelt serve --help` (mirrored verbatim in docs/CLI.md) must name exactly
// the routes declared in api/openapi.yaml, the generated API's source of truth.
func TestServeHelpMatchesOpenAPISpec(t *testing.T) {
	fromSpec := routesFromSpec(t)
	fromHelp := routesFromHelp()

	for r := range fromSpec {
		if !fromHelp[r] {
			t.Errorf("spec declares %q but serve --help does not document it", r)
		}
	}
	for r := range fromHelp {
		if !fromSpec[r] {
			t.Errorf("serve --help documents %q but it is not in api/openapi.yaml", r)
		}
	}
}
