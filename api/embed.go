// Package api holds the REST API contract (openapi.yaml), embedded so the server can
// serve exactly the document that ships with it.
package api

import _ "embed"

//go:embed openapi.yaml
var OpenAPI []byte
