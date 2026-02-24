package migrations

import "embed"

// FS contains all Goose SQL migrations for this service.
//
//go:embed *.sql
var FS embed.FS

