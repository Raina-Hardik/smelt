// Package api holds the OpenAPI 3 contract for the smelt HTTP API and the
// server code generated from it. openapi.yaml is the source of truth; gen.go
// is generated and must never be hand-edited.
package api

import _ "embed"

//go:generate go tool oapi-codegen -config oapi-codegen.yaml openapi.yaml

//go:embed openapi.yaml
var SpecYAML []byte
