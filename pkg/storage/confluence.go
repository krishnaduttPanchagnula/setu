package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"setu/pkg/ticket"
)

// ConfluenceClient posts pages to Confluence via the REST API
// (POST /rest/api/content with storage-format body).
type ConfluenceClient struct {
	BaseURL      string // e.g. https://company.atlassian.net
	Token        string // PAT from env, never from the config file
	SpaceKey     string
	ParentPageID string
	Client       *http.Client
}

// NewConfluenceClient builds a client with a sane timeout.
func NewConfluenceClient(baseURL, token, spaceKey, parentPageID string) *ConfluenceClient {
	return &ConfluenceClient{
		BaseURL:      strings.TrimRight(baseURL, "/"),
		Token:        token,
		SpaceKey:     spaceKey,
		ParentPageID: parentPageID,
		Client:       &http.Client{Timeout: 15 * time.Second},
	}
}

// SessionSummary is the structured input for a session summary page.
type SessionSummary struct {
	Item       ticket.WorkItem
	Markdown   string
	StartedAt  time.Time
	FinishedAt time.Time
}

// PushSummary renders a summary page and creates it in Confluence.
// It returns the new content ID.
func (c *ConfluenceClient) PushSummary(ctx context.Context, s SessionSummary) (string, error) {
	if c.BaseURL == "" || c.Token == "" {
		return "", fmt.Errorf("confluence: base URL and token are required")
	}

	title := fmt.Sprintf("AI Session %s - %s", s.Item.ID, s.FinishedAt.Format("2006-01-02"))
	body := MarkdownToStorage(s.Markdown)

	payload := map[string]any{
		"type":  "page",
		"title": title,
		"space": map[string]string{"key": c.SpaceKey},
		"body": map[string]any{
			"storage": map[string]string{
				"value":          body,
				"representation": "storage",
			},
		},
	}
	if c.ParentPageID != "" {
		payload["ancestors"] = []map[string]string{{"id": c.ParentPageID}}
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("confluence: encode payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/rest/api/content", bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("confluence: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("confluence: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("confluence: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("confluence: decode response: %w", err)
	}
	return out.ID, nil
}

// MarkdownToStorage converts a practical subset of Markdown into Confluence
// storage (XHTML) format: headings, paragraphs, bold/inline code, links,
// unordered lists and fenced code blocks. Anything else is escaped text.
func MarkdownToStorage(md string) string {
	var b strings.Builder
	lines := strings.Split(md, "\n")

	inCode := false
	inList := false

	closeList := func() {
		if inList {
			b.WriteString("</ul>")
			inList = false
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				b.WriteString("]]></ac:plain-text-body></ac:structured-macro>")
				inCode = false
			} else {
				closeList()
				b.WriteString(`<ac:structured-macro ac:name="code"><ac:plain-text-body><![CDATA[`)
				inCode = true
			}
			continue
		}
		if inCode {
			b.WriteString(cdataEscape(line))
			b.WriteString("\n")
			continue
		}

		switch {
		case trimmed == "":
			closeList()
		case strings.HasPrefix(trimmed, "### "):
			closeList()
			fmt.Fprintf(&b, "<h3>%s</h3>", inlineHTML(strings.TrimPrefix(trimmed, "### ")))
		case strings.HasPrefix(trimmed, "## "):
			closeList()
			fmt.Fprintf(&b, "<h2>%s</h2>", inlineHTML(strings.TrimPrefix(trimmed, "## ")))
		case strings.HasPrefix(trimmed, "# "):
			closeList()
			fmt.Fprintf(&b, "<h1>%s</h1>", inlineHTML(strings.TrimPrefix(trimmed, "# ")))
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			if !inList {
				b.WriteString("<ul>")
				inList = true
			}
			fmt.Fprintf(&b, "<li>%s</li>", inlineHTML(strings.TrimLeft(trimmed[2:], " \t")))
		default:
			closeList()
			fmt.Fprintf(&b, "<p>%s</p>", inlineHTML(trimmed))
		}
	}
	if inCode {
		b.WriteString("]]></ac:plain-text-body></ac:structured-macro>")
	}
	closeList()
	return b.String()
}

// cdataEscape prevents user or ticket content from terminating the CDATA
// section early (security: stored-XSS / markup breakout). The standard XML
// technique splits "]]>" so it round-trips as literal text.
func cdataEscape(s string) string {
	return strings.ReplaceAll(s, "]]>", "]]]]><![CDATA[>")
}

// inlineHTML applies inline markdown rules: code spans, bold, links and
// HTML escaping of the remaining text.
func inlineHTML(s string) string {
	// Split on `code` spans first so their contents are not re-processed.
	var b strings.Builder
	parts := strings.Split(s, "`")
	for i, part := range parts {
		if i%2 == 1 {
			fmt.Fprintf(&b, "<code>%s</code>", escapeHTML(part))
			continue
		}
		b.WriteString(boldAndLinks(part))
	}
	return b.String()
}

func boldAndLinks(s string) string {
	var b strings.Builder
	// Links: [text](url) — the surrounding text still gets bold handling.
	for {
		lb := strings.Index(s, "[")
		rb := strings.Index(s, "](")
		if lb >= 0 && rb > lb {
			end := strings.Index(s[rb:], ")")
			if end < 0 {
				break
			}
			b.WriteString(applyBold(s[:lb]))
			text := s[lb+1 : rb]
			href := s[rb+2 : rb+end]
			fmt.Fprintf(&b, `<a href="%s">%s</a>`, escapeHTML(href), escapeHTML(text))
			s = s[rb+end+1:]
			continue
		}
		break
	}
	b.WriteString(applyBold(s))
	return b.String()
}

// applyBold converts **text** to <strong> while escaping everything else.
func applyBold(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "**")
		if i < 0 {
			break
		}
		j := strings.Index(s[i+2:], "**")
		if j < 0 {
			break
		}
		b.WriteString(escapeHTML(s[:i]))
		fmt.Fprintf(&b, "<strong>%s</strong>", escapeHTML(s[i+2:i+2+j]))
		s = s[i+2+j+2:]
	}
	b.WriteString(escapeHTML(s))
	return b.String()
}

func escapeHTML(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	)
	return r.Replace(s)
}
