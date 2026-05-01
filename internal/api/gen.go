// Package api contains the HTTP handlers for the longue-vue REST API and the
// server/model code generated from api/openapi/openapi.yaml.
//
// Run `go generate ./...` (or `make generate`) after editing the OpenAPI
// specification to refresh api.gen.go.
package api

//go:generate go tool oapi-codegen -config ../../api/openapi/oapi-codegen.yaml ../../api/openapi/openapi.yaml
