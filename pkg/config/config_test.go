package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadDefaults verifies built-in defaults are applied when no file exists.
func TestLoadDefaults(t *testing.T) {
	t.Setenv("AI_HARNESS_CONFIG_DIR", t.TempDir())
	t.Setenv("JIRA_API_TOKEN", "")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Core.WorkspaceDir != ".ai-context" {
		t.Errorf("workspace_dir = %q, want .ai-context", cfg.Core.WorkspaceDir)
	}
	if cfg.Core.DefaultHarness != "claude-code" {
		t.Errorf("default_harness = %q, want claude-code", cfg.Core.DefaultHarness)
	}
	if cfg.Ticketing.Provider != "manual" {
		t.Errorf("provider = %q, want manual", cfg.Ticketing.Provider)
	}
	if cfg.Ticketing.StatusFilter != "ready-to-work" {
		t.Errorf("status_filter = %q, want ready-to-work", cfg.Ticketing.StatusFilter)
	}
	if len(cfg.Harnesses) == 0 {
		t.Error("default harnesses map is empty")
	}
}

// TestLoadFromYaml verifies viper maps the PRD section 2.2 schema into the struct.
func TestLoadFromYaml(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
core:
  default_harness: "ollama"
  workspace_dir: ".ai-ctx"
ticketing:
  provider: "jira"
  status_filter: "In Progress"
  assignee: "dev@company.com"
  jira:
    url: "https://company.atlassian.net"
    username: "dev@company.com"
confluence:
  enabled: true
  space_key: "ENG"
  parent_page_id: "123456789"
harnesses:
  ollama:
    command: "ollama"
    args: ["run", "qwen2.5-coder"]
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Core.DefaultHarness != "ollama" {
		t.Errorf("default_harness = %q, want ollama", cfg.Core.DefaultHarness)
	}
	if cfg.Core.WorkspaceDir != ".ai-ctx" {
		t.Errorf("workspace_dir = %q, want .ai-ctx", cfg.Core.WorkspaceDir)
	}
	if cfg.Ticketing.Provider != "jira" {
		t.Errorf("provider = %q, want jira", cfg.Ticketing.Provider)
	}
	if cfg.Ticketing.Jira.URL != "https://company.atlassian.net" {
		t.Errorf("jira url = %q", cfg.Ticketing.Jira.URL)
	}
	if !cfg.Confluence.Enabled || cfg.Confluence.SpaceKey != "ENG" {
		t.Errorf("confluence not mapped: %+v", cfg.Confluence)
	}
	spec := cfg.HarnessSpec("ollama")
	if spec.Command != "ollama" || len(spec.Args) != 2 {
		t.Errorf("ollama spec = %+v", spec)
	}
}

// TestBindEnvSecrets verifies PATs come from env vars, not the config file.
func TestBindEnvSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
ticketing:
  provider: "jira"
  jira:
    url: "https://x.atlassian.net"
    username: "u"
confluence:
  enabled: true
  space_key: "ENG"
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AI_HARNESS_JIRA_TOKEN", "jira-pat-123")
	t.Setenv("AI_HARNESS_CONFLUENCE_TOKEN", "conf-pat-456")
	t.Setenv("JIRA_API_TOKEN", "")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Ticketing.Jira.Token != "jira-pat-123" {
		t.Errorf("jira token = %q, want env value", cfg.Ticketing.Jira.Token)
	}
	if cfg.Confluence.Token != "conf-pat-456" {
		t.Errorf("confluence token = %q, want env value", cfg.Confluence.Token)
	}
}

// TestBindEnvSecretsIgnoresConfigFileToken enforces the PRD 2.2 rule that
// PATs come ONLY from environment variables: a token written in the config
// file must never reach the effective configuration.
func TestBindEnvSecretsIgnoresConfigFileToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
ticketing:
  provider: "jira"
  jira:
    url: "https://x.atlassian.net"
    username: "u"
    token: "file-jira-secret"
confluence:
  enabled: true
  space_key: "ENG"
  token: "file-confluence-secret"
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AI_HARNESS_JIRA_TOKEN", "")
	t.Setenv("JIRA_API_TOKEN", "")
	t.Setenv("AI_HARNESS_CONFLUENCE_TOKEN", "")
	t.Setenv("CONFLUENCE_API_TOKEN", "")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Ticketing.Jira.Token != "" {
		t.Errorf("config-file jira token leaked into config: %q", cfg.Ticketing.Jira.Token)
	}
	if cfg.Confluence.Token != "" {
		t.Errorf("config-file confluence token leaked into config: %q", cfg.Confluence.Token)
	}
}

// TestAlternateEnvVarNames verifies the secondary env names from PRD 2.2.
func TestAlternateEnvVarNames(t *testing.T) {
	t.Setenv("AI_HARNESS_CONFIG_DIR", t.TempDir())
	t.Setenv("AI_HARNESS_JIRA_TOKEN", "")
	t.Setenv("JIRA_API_TOKEN", "alt-jira-token")
	t.Setenv("AI_HARNESS_AZURE_PAT", "")
	t.Setenv("AZURE_DEVOPS_PAT", "alt-azure-pat")
	t.Setenv("AI_HARNESS_CONFLUENCE_TOKEN", "")
	t.Setenv("CONFLUENCE_API_TOKEN", "alt-conf-token")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Ticketing.Jira.Token != "alt-jira-token" {
		t.Errorf("JIRA_API_TOKEN not bound: %q", cfg.Ticketing.Jira.Token)
	}
	if cfg.Ticketing.Azure.PAT != "alt-azure-pat" {
		t.Errorf("AZURE_DEVOPS_PAT not bound: %q", cfg.Ticketing.Azure.PAT)
	}
	if cfg.Confluence.Token != "alt-conf-token" {
		t.Errorf("CONFLUENCE_API_TOKEN not bound: %q", cfg.Confluence.Token)
	}
}

// TestConfigExampleRepoFileMode0600 keeps the checked-in example config at
// the PRD-mandated restrictive permission (it documents where secrets go).
func TestConfigExampleRepoFileMode0600(t *testing.T) {
	path := filepath.Join("..", "..", "config.example.yaml")
	info, err := os.Stat(path)
	if err != nil {
		t.Skipf("example config not present: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config.example.yaml perms = %o, want 600", perm)
	}
}

// TestLoadExplicitMissingFile ensures an explicit --config path must exist.
func TestLoadExplicitMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected error for missing explicit config file")
	}
}

// TestHarnessSpecFallback ensures unknown harness names fall back to the raw name.
func TestHarnessSpecFallback(t *testing.T) {
	cfg := &Config{}
	spec := cfg.HarnessSpec("my-custom-agent")
	if spec.Command != "my-custom-agent" {
		t.Errorf("fallback command = %q", spec.Command)
	}
}

// TestWriteExample ensures idempotent example config generation with 0600 perms.
func TestWriteExample(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	if err := WriteExample(path); err != nil {
		t.Fatalf("WriteExample: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config perms = %o, want 600 (secrets file)", perm)
	}
	if err := WriteExample(path); err == nil {
		t.Error("second WriteExample should fail (do not clobber user config)")
	}
}
