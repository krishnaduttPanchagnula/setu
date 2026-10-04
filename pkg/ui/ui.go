// Package ui serves the knowledge base web interface: a single-page view over
// every previous run discovered under .ai-context workspaces.
package ui

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"

	"setu/pkg/kb"
)

//go:embed index.html
var indexHTML []byte

// Provider produces a fresh knowledge base snapshot on every request, so the
// UI reflects runs persisted while the server is up.
type Provider func() (*kb.KB, error)

// NewHandler wires the embedded page and the JSON API.
func NewHandler(discover Provider) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})
	mux.HandleFunc("GET /api/kb", func(w http.ResponseWriter, r *http.Request) {
		k, err := discover()
		if err != nil {
			http.Error(w, fmt.Sprintf("scan failed: %v", err), http.StatusInternalServerError)
			return
		}
		// Pre-render requirement markdown server-side; the page injects it
		// into the DOM only after escaping, this is already safe HTML.
		type sessionView struct {
			kb.Session
			RequirementHTML string `json:"requirementHtml"`
		}
		views := make([]sessionView, len(k.Sessions))
		for i, s := range k.Sessions {
			views[i] = sessionView{Session: s, RequirementHTML: RenderMarkdown(s.Requirement)}
		}
		payload := struct {
			*kb.KB
			Sessions []sessionView `json:"sessions"`
		}{KB: k, Sessions: views}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(payload); err != nil {
			return
		}
	})
	return mux
}

// DiscoverRoots is the Provider constructor used by the CLI: it scans the
// given roots (absolute-ized and de-duplicated).
func DiscoverRoots(roots []string) Provider {
	return func() (*kb.KB, error) {
		clean := make([]string, 0, len(roots))
		seen := map[string]bool{}
		for _, r := range roots {
			abs, err := filepath.Abs(r)
			if err != nil {
				continue
			}
			if seen[abs] {
				continue
			}
			seen[abs] = true
			clean = append(clean, abs)
		}
		sort.Strings(clean)
		k, err := kb.Discover(clean)
		if err != nil {
			return nil, err
		}
		if k.Roots != nil {
			k.Roots = clean
		}
		return k, nil
	}
}

// Summary renders a one-line scan result for CLI startup output.
func Summary(k *kb.KB) string {
	if k == nil || len(k.Sessions) == 0 {
		return "no runs found (no .ai-context workspaces under the given directories)"
	}
	return fmt.Sprintf("%d run(s), %d work item(s), %d workspace(s)",
		len(k.Sessions), k.Items, len(k.Workspaces))
}
