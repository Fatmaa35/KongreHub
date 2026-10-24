// Package migrations embeds the goose SQL migrations into the binary so the
// `migrate` compose service needs no extra files.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
