package ui

import (
	"html"
	"regexp"
	"strings"
)

var (
	headingRe   = regexp.MustCompile(`^(#{1,4})\s+(.*)$`)
	unorderedRe = regexp.MustCompile(`^[-*]\s+(.*)$`)
	orderedRe   = regexp.MustCompile(`^\d+\.\s+(.*)$`)
	linkRe      = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	boldRe      = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	italicRe    = regexp.MustCompile(`\*([^*]+)\*`)
	codeRe      = regexp.MustCompile("`([^`]+)`")
)

// RenderMarkdown converts a small markdown subset to safe HTML. The input is
// HTML-escaped first, so raw markup inside documents cannot inject scripts
// into the UI (stored-XSS defense, mirroring the Confluence escape rules).
func RenderMarkdown(md string) string {
	var b strings.Builder
	var listTag string
	inCode := false
	var code []string

	closeList := func() {
		if listTag != "" {
			b.WriteString("</" + listTag + ">\n")
			listTag = ""
		}
	}

	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimRight(raw, "\r")

		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inCode {
				b.WriteString("<pre><code>" + html.EscapeString(strings.Join(code, "\n")) + "</code></pre>\n")
				code = nil
				inCode = false
			} else {
				closeList()
				inCode = true
			}
			continue
		}
		if inCode {
			code = append(code, line)
			continue
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			closeList()
			continue
		}

		if m := headingRe.FindStringSubmatch(trimmed); m != nil {
			closeList()
			level := len(m[1]) + 1 // h1 -> h2 ... keeps the page title dominant
			if level > 5 {
				level = 5
			}
			b.WriteString("<h" + string(rune('0'+level)) + ">" + inline(m[2]) + "</h" + string(rune('0'+level)) + ">\n")
			continue
		}
		if trimmed == "---" || trimmed == "***" {
			closeList()
			b.WriteString("<hr>\n")
			continue
		}
		if m := unorderedRe.FindStringSubmatch(trimmed); m != nil {
			if listTag != "ul" {
				closeList()
				b.WriteString("<ul>\n")
				listTag = "ul"
			}
			b.WriteString("<li>" + inline(m[1]) + "</li>\n")
			continue
		}
		if m := orderedRe.FindStringSubmatch(trimmed); m != nil {
			if listTag != "ol" {
				closeList()
				b.WriteString("<ol>\n")
				listTag = "ol"
			}
			b.WriteString("<li>" + inline(m[1]) + "</li>\n")
			continue
		}

		closeList()
		b.WriteString("<p>" + inline(line) + "</p>\n")
	}
	if inCode {
		b.WriteString("<pre><code>" + html.EscapeString(strings.Join(code, "\n")) + "</code></pre>\n")
	}
	closeList()
	return b.String()
}

// inline applies escaping and inline markdown to a single line.
func inline(s string) string {
	s = html.EscapeString(s)
	s = linkRe.ReplaceAllStringFunc(s, func(m string) string {
		parts := linkRe.FindStringSubmatch(m)
		if len(parts) != 3 {
			return m
		}
		href := parts[2]
		// Only safe URL schemes.
		lower := strings.ToLower(href)
		if strings.HasPrefix(lower, "javascript:") || strings.HasPrefix(lower, "data:") {
			href = "#"
		}
		return `<a href="` + href + `" rel="noreferrer noopener" target="_blank">` + parts[1] + `</a>`
	})
	s = boldRe.ReplaceAllString(s, "<strong>$1</strong>")
	s = italicRe.ReplaceAllString(s, "<em>$1</em>")
	s = codeRe.ReplaceAllString(s, "<code>$1</code>")
	return s
}
