package ticket

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// jiraPayload builds a realistic Jira search response with n issues.
func jiraPayload(n int) []byte {
	issues := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		issues = append(issues, map[string]any{
			"key": fmt.Sprintf("PROJ-%d", i),
			"fields": map[string]any{
				"summary":     fmt.Sprintf("Issue title %d", i),
				"description": "As a developer I want a long description with several sentences so that the benchmark is realistic.",
			},
		})
	}
	raw, _ := json.Marshal(map[string]any{"issues": issues})
	return raw
}

// BenchmarkJiraGetActiveWorkItems measures the REST round trip + decode for a
// 50-ticket backlog (the interactive `setu start` query).
func BenchmarkJiraGetActiveWorkItems(b *testing.B) {
	body := jiraPayload(50)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	p := NewJiraProvider(server.URL, "u", "t")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		items, err := p.GetActiveWorkItems("dev@company.com", "ready-to-work")
		if err != nil {
			b.Fatalf("GetActiveWorkItems: %v", err)
		}
		if len(items) != 50 {
			b.Fatalf("got %d items", len(items))
		}
	}
}

// BenchmarkManualProviderFromFlag measures the offline path.
func BenchmarkManualProviderFromFlag(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := NewFromFlag("PROJ-1", "Title", "Requirement")
		items, err := p.GetActiveWorkItems("", "")
		if err != nil || len(items) != 1 {
			b.Fatalf("provider: %v %v", err, items)
		}
	}
}
