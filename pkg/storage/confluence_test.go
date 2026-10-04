package storage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"setu/pkg/ticket"
)

func TestMarkdownToStorageHeadingsAndParagraphs(t *testing.T) {
	md := "# Title\n\n## Section\n\nSome paragraph text.\n"
	html := MarkdownToStorage(md)

	if !strings.Contains(html, "<h1>Title</h1>") {
		t.Errorf("missing h1: %s", html)
	}
	if !strings.Contains(html, "<h2>Section</h2>") {
		t.Errorf("missing h2: %s", html)
	}
	if !strings.Contains(html, "<p>Some paragraph text.</p>") {
		t.Errorf("missing paragraph: %s", html)
	}
}

func TestMarkdownToStorageInline(t *testing.T) {
	html := MarkdownToStorage("Use `go test` for **unit** tests and see [docs](https://x.dev).")

	if !strings.Contains(html, "<code>go test</code>") {
		t.Errorf("missing code span: %s", html)
	}
	if !strings.Contains(html, "<strong>unit</strong>") {
		t.Errorf("missing bold: %s", html)
	}
	if !strings.Contains(html, `<a href="https://x.dev">docs</a>`) {
		t.Errorf("missing link: %s", html)
	}
}

func TestMarkdownToStorageCodeBlock(t *testing.T) {
	md := "```go\nfunc main() {}\n```"
	html := MarkdownToStorage(md)

	if !strings.Contains(html, "ac:structured-macro") {
		t.Errorf("missing code macro: %s", html)
	}
	if !strings.Contains(html, "func main() {}") {
		t.Errorf("code body lost: %s", html)
	}
}

func TestMarkdownToStorageList(t *testing.T) {
	html := MarkdownToStorage("- one\n- two")
	if !strings.Contains(html, "<ul>") || !strings.Contains(html, "</ul>") {
		t.Errorf("missing ul: %s", html)
	}
	if !strings.Contains(html, "<li>one</li>") || !strings.Contains(html, "<li>two</li>") {
		t.Errorf("missing li: %s", html)
	}
}

// TestMarkdownToStorageEscapesHTML ensures user/ticket content cannot inject
// markup into Confluence (security: stored XSS).
func TestMarkdownToStorageEscapesHTML(t *testing.T) {
	html := MarkdownToStorage("Hello <script>alert('xss')</script> world")
	if strings.Contains(html, "<script>") {
		t.Errorf("script tag not escaped: %s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("expected escaped script tag: %s", html)
	}
}

// TestMarkdownToStorageCodeBlockEscapesCDataBreakout ensures ticket content
// inside a fenced block cannot terminate the CDATA section early and inject
// markup after it (security: stored-XSS via ]] breakout).
func TestMarkdownToStorageCodeBlockEscapesCDataBreakout(t *testing.T) {
	md := "```go\nx := 1 ]]><script>alert('xss')</script>\n```"
	html := MarkdownToStorage(md)

	if strings.Contains(html, "]]><script>") {
		t.Errorf("CDATA breakout not escaped: %s", html)
	}
	if !strings.Contains(html, "]]></ac:plain-text-body></ac:structured-macro>") {
		t.Errorf("missing structural CDATA closer: %s", html)
	}
	// The literal payload must survive the round trip split intact:
	// "]]" stays before the injected "]]>" split point.
	if !strings.Contains(html, "<script>alert('xss')</script>") {
		t.Errorf("code body content lost: %s", html)
	}
}

// TestPushSummary exercises the Confluence REST POST contract.
func TestPushSummary(t *testing.T) {
	var gotAuth, gotCT string
	var payload map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/content" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("payload not JSON: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "999888"})
	}))
	defer server.Close()

	c := NewConfluenceClient(server.URL, "conf-pat", "ENG", "123456789")
	id, err := c.PushSummary(context.Background(), SessionSummary{
		Item:       ticket.WorkItem{ID: "PROJ-1", Title: "T", Requirement: "R"},
		Markdown:   "# PROJ-1\n\nRequirement text.",
		StartedAt:  time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC),
		FinishedAt: time.Date(2026, 10, 4, 11, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("PushSummary: %v", err)
	}
	if id != "999888" {
		t.Errorf("id = %q", id)
	}
	if gotAuth != "Bearer conf-pat" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q", gotCT)
	}
	if payload["type"] != "page" {
		t.Errorf("type = %v", payload["type"])
	}
	space, _ := payload["space"].(map[string]any)
	if space["key"] != "ENG" {
		t.Errorf("space key = %v", space["key"])
	}
	ancestors, _ := payload["ancestors"].([]any)
	if len(ancestors) != 1 {
		t.Errorf("ancestors = %v", payload["ancestors"])
	}
	title, _ := payload["title"].(string)
	if !strings.Contains(title, "PROJ-1") {
		t.Errorf("title = %q, want work item ID", title)
	}
}

// TestPushSummaryRequiresCredentials guards against silent token-less pushes.
func TestPushSummaryRequiresCredentials(t *testing.T) {
	c := NewConfluenceClient("", "", "ENG", "")
	if _, err := c.PushSummary(context.Background(), SessionSummary{}); err == nil {
		t.Fatal("expected error without base URL/token")
	}
}

// TestPushSummaryServerError surfaces HTTP failures.
func TestPushSummaryServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "quota exceeded", http.StatusTooManyRequests)
	}))
	defer server.Close()

	c := NewConfluenceClient(server.URL, "t", "ENG", "")
	_, err := c.PushSummary(context.Background(), SessionSummary{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error = %v, want status code", err)
	}
}
