// Package migrations embeds the SQL migrations applied by longue-vue at startup
// or by an offline tool.
package migrations

import "embed"

// FS is the filesystem containing every migration in this directory.
//
//go:embed *.sql
var FS embed.FS
