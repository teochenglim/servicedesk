// Package web embeds the static frontend (templates + vendored JS/CSS) so the
// whole ServiceDesk UI ships inside a single Go binary with no external assets.
package web

import "embed"

//go:embed templates/*.html
var TemplatesFS embed.FS

//go:embed static
var StaticFS embed.FS
