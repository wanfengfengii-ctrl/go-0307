// Package web embeds the deterministically built frontend assets so the Go
// service can serve the operations page without an external web server.
package web

import "embed"

//go:embed dist
var FS embed.FS
