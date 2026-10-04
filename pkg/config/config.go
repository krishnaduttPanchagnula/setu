// Package config implements the Config Manager described in PRD section 2.1/2.2.
// It reads ~/.config/ai-harness/config.yaml through viper and binds secrets
// (PATs) from environment variables so tokens never live in the file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// HarnessSpec declares how to launch a delegated AI harness.
type HarnessSpec struct {
	Command string   `mapstructure:"command"`
	Args    []string `mapstructure:"args"`
}

// JiraConfig holds the Atlassian REST endpoint settings.
type JiraConfig struct {
	URL      string `mapstructure:"url"`
	Username string `mapstructure:"username"`
	Token    string `mapstructure:"token"` // via AI_HARNESS_JIRA_TOKEN / JIRA_API_TOKEN
}

// AzureConfig holds the Azure DevOps REST endpoint settings.
type AzureConfig struct {
	URL     string `mapstructure:"url"`
	Org     string `mapstructure:"org"`
	Project string `mapstructure:"project"`
	PAT     string `mapstructure:"pat"` // via AI_HARNESS_AZURE_PAT / AZURE_DEVOPS_PAT
}

// ConfluenceConfig holds the Confluence REST endpoint settings.
type ConfluenceConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	BaseURL      string `mapstructure:"base_url"`
	SpaceKey     string `mapstructure:"space_key"`
	ParentPageID string `mapstructure:"parent_page_id"`
	Token        string `mapstructure:"token"` // via AI_HARNESS_CONFLUENCE_TOKEN / CONFLUENCE_API_TOKEN
}

// Config is the root struct mapped from config.yaml (PRD section 2.2).
type Config struct {
	Core struct {
		DefaultHarness string `mapstructure:"default_harness"`
		WorkspaceDir   string `mapstructure:"workspace_dir"`
		Author         struct {
			Name  string `mapstructure:"name"`
			Email string `mapstructure:"email"`
		} `mapstructure:"author"`
	} `mapstructure:"core"`

	Ticketing struct {
		Provider     string      `mapstructure:"provider"` // jira | azure | manual
		StatusFilter string      `mapstructure:"status_filter"`
		Assignee     string      `mapstructure:"assignee"`
		Jira         JiraConfig  `mapstructure:"jira"`
		Azure        AzureConfig `mapstructure:"azure"`
	} `mapstructure:"ticketing"`

	Confluence ConfluenceConfig `mapstructure:"confluence"`

	Harnesses map[string]HarnessSpec `mapstructure:"harnesses"`
}

// DefaultConfigDir returns ~/.config/ai-harness (overridable for tests).
func DefaultConfigDir() string {
	if dir := os.Getenv("AI_HARNESS_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "ai-harness")
	}
	return filepath.Join(home, ".config", "ai-harness")
}

// DefaultPath returns the default config.yaml location.
func DefaultPath() string {
	return filepath.Join(DefaultConfigDir(), "config.yaml")
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("core.default_harness", "claude-code")
	v.SetDefault("core.workspace_dir", ".ai-context")
	v.SetDefault("ticketing.provider", "manual")
	v.SetDefault("ticketing.status_filter", "ready-to-work")
	v.SetDefault("confluence.enabled", false)
	v.SetDefault("harnesses", map[string]any{
		"claude-code": map[string]any{"command": "claude", "args": []string{}},
		"copilot-cli": map[string]any{"command": "gh", "args": []string{"copilot", "suggest"}},
		"ollama":      map[string]any{"command": "ollama", "args": []string{"run", "qwen2.5-coder"}},
		"lmstudio":    map[string]any{"command": "lms", "args": []string{"chat"}},
	})
}

// bindSecrets wires PATs to environment variables (PRD section 2.2).
// Multiple names are supported: the first one that is set wins.
func bindSecrets(v *viper.Viper) error {
	pairs := [][2]string{
		{"ticketing.jira.token", "AI_HARNESS_JIRA_TOKEN"},
		{"ticketing.jira.token", "JIRA_API_TOKEN"},
		{"ticketing.azure.pat", "AI_HARNESS_AZURE_PAT"},
		{"ticketing.azure.pat", "AZURE_DEVOPS_PAT"},
		{"confluence.token", "AI_HARNESS_CONFLUENCE_TOKEN"},
		{"confluence.token", "CONFLUENCE_API_TOKEN"},
	}
	for _, p := range pairs {
		if err := v.BindEnv(p[0], p[1]); err != nil {
			return fmt.Errorf("config: bind %s: %w", p[1], err)
		}
	}
	return nil
}

// Load reads the configuration file at path. An empty path resolves to
// DefaultPath(). A missing default file yields built-in defaults, while an
// explicitly requested file must exist and parse.
func Load(path string) (*Config, error) {
	explicit := path != ""
	if !explicit {
		path = DefaultPath()
	}

	v := viper.New()
	setDefaults(v)
	if err := bindSecrets(v); err != nil {
		return nil, err
	}
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !explicit && errors.As(err, &notFound) {
			return fromViper(v), nil
		}
		// viper may also report os.ErrNotExist for SetConfigFile paths.
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return fromViper(v), nil
		}
		return nil, fmt.Errorf("config: reading %s: %w", path, err)
	}
	return fromViper(v), nil
}

func fromViper(v *viper.Viper) *Config {
	c := &Config{}
	if err := v.Unmarshal(c); err != nil {
		// Unmarshal only fails on structural type conflicts; surface as a
		// panic-free fallback to defaults rather than nil derefs later.
		return &Config{}
	}
	if c.Core.WorkspaceDir == "" {
		c.Core.WorkspaceDir = ".ai-context"
	}
	if c.Core.DefaultHarness == "" {
		c.Core.DefaultHarness = "claude-code"
	}
	if c.Ticketing.StatusFilter == "" {
		c.Ticketing.StatusFilter = "ready-to-work"
	}
	if c.Harnesses == nil {
		d := &Config{}
		setDefaults(v)
		_ = v.Unmarshal(d)
		c.Harnesses = d.Harnesses
	}
	enforceEnvOnlySecrets(v, c)
	return c
}

// enforceEnvOnlySecrets implements the PRD 2.2 rule that PATs are loaded
// "via viper.BindEnv" and live only in environment variables: any token
// found in the config file itself is discarded (with a warning) and the
// effective value is re-read from the bound environment variables.
func enforceEnvOnlySecrets(v *viper.Viper, c *Config) {
	warnFileSecret := func(key string) {
		if v.InConfig(key) {
			fmt.Fprintf(os.Stderr,
				"setu: warning: %s found in config file is IGNORED; set the token via environment variable instead\n",
				key)
		}
	}
	warnFileSecret("ticketing.jira.token")
	warnFileSecret("ticketing.azure.pat")
	warnFileSecret("confluence.token")

	c.Ticketing.Jira.Token = firstSetEnv("AI_HARNESS_JIRA_TOKEN", "JIRA_API_TOKEN")
	c.Ticketing.Azure.PAT = firstSetEnv("AI_HARNESS_AZURE_PAT", "AZURE_DEVOPS_PAT")
	c.Confluence.Token = firstSetEnv("AI_HARNESS_CONFLUENCE_TOKEN", "CONFLUENCE_API_TOKEN")
}

// firstSetEnv returns the first non-empty environment variable value.
func firstSetEnv(names ...string) string {
	for _, n := range names {
		if val := os.Getenv(n); val != "" {
			return val
		}
	}
	return ""
}

// HarnessSpec returns the launch spec for name, falling back to name-as-command
// so users can point at any binary via config without code changes.
func (c *Config) HarnessSpec(name string) HarnessSpec {
	if spec, ok := c.Harnesses[name]; ok && spec.Command != "" {
		return spec
	}
	return HarnessSpec{Command: name}
}

// WriteExample atomically writes an example config.yaml when none exists.
func WriteExample(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("config: %s already exists", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("config: mkdir: %w", err)
	}
	const example = `# ai-harness configuration (PRD section 2.2)
core:
  default_harness: "claude-code" # ollama | lmstudio | claude-code | copilot-cli
  workspace_dir: ".ai-context"

ticketing:
  provider: "jira" # jira | azure | manual
  status_filter: "ready-to-work"
  assignee: "dev@company.com"
  jira:
    url: "https://company.atlassian.net"
    username: "dev@company.com"
    # token: via AI_HARNESS_JIRA_TOKEN or JIRA_API_TOKEN
  azure:
    url: "https://dev.azure.com/company"
    org: "company"
    project: "proj"
    # pat: via AI_HARNESS_AZURE_PAT or AZURE_DEVOPS_PAT

confluence:
  enabled: false
  base_url: "https://company.atlassian.net"
  space_key: "ENG"
  parent_page_id: "123456789"
  # token: via AI_HARNESS_CONFLUENCE_TOKEN or CONFLUENCE_API_TOKEN

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
	if err := os.WriteFile(path, []byte(example), 0o600); err != nil {
		return fmt.Errorf("config: write example: %w", err)
	}
	return nil
}
