package ui

import (
	"strings"
	"testing"
)

func TestRenderMarkdownBasics(t *testing.T) {
	md := "# Title\n\nSome **bold** and *italic* with `code`.\n\n" +
		"- first\n- second\n\n1. one\n2. two\n\n```go\nfmt.Println(\"x\")\n```\n"
	html := RenderMarkdown(md)

	for _, want := range []string{
		"<h2>Title</h2>",
		"<strong>bold</strong>",
		"<em>italic</em>",
		"<code>code</code>",
		"<ul>", "<li>first</li>", "</ul>",
		"<ol>", "<li>one</li>", "</ol>",
		"<pre><code>fmt.Println(&#34;x&#34;)</code></pre>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered HTML missing %q:\n%s", want, html)
		}
	}
}

func TestRenderMarkdownEscapesRawHTML(t *testing.T) {
	html := RenderMarkdown("hello <script>alert(1)</script> <img src=x onerror=alert(1)>")
	if strings.Contains(html, "<script>") {
		t.Errorf("script tag not escaped:\n%s", html)
	}
	if strings.Contains(html, "onerror=") {
		// The attribute text may appear but only inside escaped text nodes;
		// ensure no raw <img tag survived.
		if strings.Contains(html, "<img") {
			t.Errorf("raw img tag not escaped:\n%s", html)
		}
	}
}

func TestRenderMarkdownBlocksJavascriptLinks(t *testing.T) {
	html := RenderMarkdown("[click](javascript:alert(1))")
	if strings.Contains(html, `href="javascript:`) {
		t.Errorf("javascript: href not neutralized:\n%s", html)
	}
	html = RenderMarkdown("[docs](https://example.com/a?b=1&c=2)")
	if !strings.Contains(html, `href="https://example.com/a?b=1&amp;c=2"`) {
		t.Errorf("safe link not rendered correctly:\n%s", html)
	}
}

func TestRenderMarkdownUnclosedCodeFence(t *testing.T) {
	html := RenderMarkdown("```\nunclosed block\nstill code")
	if !strings.Contains(html, "<pre><code>unclosed block\nstill code</code></pre>") {
		t.Errorf("unclosed fence not flushed:\n%s", html)
	}
}

func TestRenderMarkdownEmpty(t *testing.T) {
	if got := RenderMarkdown(""); got != "" {
		t.Errorf("RenderMarkdown(\"\") = %q, want empty", got)
	}
}
