// Package swagger embeds the Swagger UI 5.x distribution and the
// longue-vue OpenAPI spec, and exposes HTTP handlers for serving both
// (UIHandler for the shell, OpenAPISpecHandler for the spec).
// See ADR-0025 and docs/superpowers/specs/2026-05-15-swagger-api-docs-design.md.
package swagger

import "embed"

//go:embed index.html
var indexHTML []byte

//go:embed init.js
var initJS []byte

//go:embed all:dist
var distFS embed.FS

//go:embed openapi.yaml
var openapiYAML []byte
