package context

import (
	"fmt"
	"os"
	"testing"

	"setu/pkg/ticket"
)

// BenchmarkBuildAll measures the workspace templating cost per session start
// (PRD 1.2 step 4). Runs against a temp workspace per iteration.
func BenchmarkBuildAll(b *testing.B) {
	orig, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	dir := b.TempDir()
	if err := os.Chdir(dir); err != nil {
		b.Fatal(err)
	}
	defer os.Chdir(orig)

	builder := NewBuilder(".ai-context")
	item := ticket.WorkItem{
		ID:          "PROJ-BENCH",
		Title:       "Benchmark item",
		Requirement: "A sufficiently realistic requirement body with **markdown** and `code`.",
		URL:         "https://example.atlassian.net/browse/PROJ-BENCH",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		item.ID = fmt.Sprintf("PROJ-B%d", i)
		if _, err := builder.BuildAll(item, "claude-code"); err != nil {
			b.Fatalf("BuildAll: %v", err)
		}
	}
}

// BenchmarkBuildFeatureFile isolates the single-file write path.
func BenchmarkBuildFeatureFile(b *testing.B) {
	orig, _ := os.Getwd()
	dir := b.TempDir()
	_ = os.Chdir(dir)
	defer os.Chdir(orig)

	builder := NewBuilder(".ai-context")
	item := ticket.WorkItem{ID: "PROJ-F", Title: "T", Requirement: "R", URL: "https://x"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := builder.BuildFeatureFile(item, nil); err != nil {
			b.Fatalf("BuildFeatureFile: %v", err)
		}
	}
}
