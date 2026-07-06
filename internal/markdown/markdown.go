package markdown

import (
	"bytes"
	"html/template"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

var renderer = goldmark.New(
	goldmark.WithExtensions(extension.GFM), // tables, strikethrough, fenced code blocks
)

// Render converts Markdown (including fenced code blocks) to sanitized HTML.
// Goldmark HTML-escapes text content by default; syntax highlighting of the
// resulting <pre><code class="language-x"> blocks is applied client-side by highlight.js.
func Render(src string) template.HTML {
	var buf bytes.Buffer
	if err := renderer.Convert([]byte(src), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(src))
	}
	return template.HTML(buf.String())
}
