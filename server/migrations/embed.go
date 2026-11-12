// Package migrations embeds the schema files so the binary can apply them.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
