// Package docs carries the API documents occulited serves (openccu-lite task 298). They are
// generated from the route table and the handlers' source by internal/httpapi's tests and
// committed here, where the release job takes them from as well.
package docs

import _ "embed"

// OpenAPI is docs/openapi.json: the OpenAPI 3.1 document of the REST API, its info.version "dev".
//
//go:embed openapi.json
var OpenAPI []byte
