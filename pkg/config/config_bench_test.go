package config

import (
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkLoad measures config parse time for the PRD 2.2 schema, the cost
// paid on every `setu` invocation.
func BenchmarkLoad(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
core:
  default_harness: "claude-code"
  workspace_dir: ".ai-context"
ticketing:
  provider: "jira"
  status_filter: "ready-to-work"
  assignee: "dev@company.com"
  jira:
    url: "https://company.atlassian.net"
    username: "dev@company.com"
confluence:
  enabled: true
  space_key: "ENG"
  parent_page_id: "123456789"
harnesses:
  claude-code:
    command: "claude"
    args: []
  copilot-cli:
    command: "gh"
    args: ["copilot", "suggest"]
  ollama:
    command: "ollama"
    args: ["run", "qwen2.5-coder"]
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cfg, err := Load(path)
		if err != nil {
			b.Fatalf("Load: %v", err)
		}
		if cfg.Core.DefaultHarness != "claude-code" {
			b.Fatal("wrong value")
		}
	}
}
