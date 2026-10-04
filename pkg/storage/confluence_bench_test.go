package storage

import (
	"strings"
	"testing"
)

const benchMarkdown = `# Session Summary PROJ-123

## What changed

- Added the **token refresh** flow
- Wired retry logic with ` + "`backoff`" + `

## Notes

See [the design doc](https://wiki.example.com/design) for details.

` + "```" + `go
func Refresh() error { return nil }
` + "```" + `
`

// BenchmarkMarkdownToStorage measures the Confluence storage-format
// conversion cost for a realistic session summary.
func BenchmarkMarkdownToStorage(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		html := MarkdownToStorage(benchMarkdown)
		if i == 0 && !strings.Contains(html, "<h1>") {
			b.Fatal("conversion lost headings")
		}
	}
}

// BenchmarkEscapeHTML measures the XSS-guard path in isolation.
func BenchmarkEscapeHTML(b *testing.B) {
	s := `<script>alert("xss")</script> & more`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = escapeHTML(s)
	}
}
