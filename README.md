# AI Harness Orchestrator CLI (`setu`)

A cross-platform Go CLI that streamlines local AI development workflows by
bridging issue trackers (Jira / Azure DevOps), context storage (Git /
Confluence), and native AI agent harnesses (Claude Code, GitHub Copilot CLI,
Ollama, LM Studio).

Implements the **delegator (orchestrator) pattern** from the PRD: the CLI
resolves context, templates the workspace, and shells out (`os/exec`) to the
native harness — which handles its own authentication, OAuth, tokens, and
session loops. The CLI never touches LLM APIs.

## Core user flow (PRD 1.2)

```text
setu start
   │
   ├─ 1. Load ~/.config/ai-harness/config.yaml (viper + env-bound PATs)
   ├─ 2. Query Jira / Azure DevOps for tickets (ready-to-work)
   ├─ 3. Interactive terminal menu selection
   ├─ 4. Generate workspace context files (.ai-context/, CLAUDE.md, ...)
   ├─ 5. Delegate: exec the harness with stdin/stdout/stderr bound
   └─ 6. On exit: session log, git auto-commit, optional Confluence push
```

## Install

**From a release** (prebuilt Linux amd64 binary):

```sh
curl -L https://github.com/krishnaduttPanchagnula/setu/releases/download/v0.1.0/setu-linux-amd64 -o setu
chmod +x setu && sudo mv setu /usr/local/bin/
```

**From source:**

```sh
go build        # produces ./setu (binary is named after the module)
```

Optionally put it on your `PATH`:

```sh
go install .    # installs as `setu` into GOBIN/GOPATH/bin
# or
sudo cp setu /usr/local/bin/
```

## Quick start

```sh
# Write an example config
./setu config init

# Add your PATs via environment (never in the config file)
export JIRA_API_TOKEN=...            # or AI_HARNESS_JIRA_TOKEN
export CONFLUENCE_API_TOKEN=...      # or AI_HARNESS_CONFLUENCE_TOKEN

# List ready-to-work tickets
./setu list

# Start a session: pick a ticket, generate context, launch claude
./setu start

# Offline / manual mode without any tracker
./setu start --manual --ticket PROJ-1 --title "My task" \
  --requirement "Implement the thing" --dry-run
```

## Commands

| Command | Description |
|---|---|
| `setu start` | Full flow: resolve → select → template → delegate → persist |
| `setu list` | Print matching work items without starting a session |
| `setu config init` | Write `~/.config/ai-harness/config.yaml` example |
| `setu version` | Print version |

### Flags (`start` / `list`)

`--config <path>`, `--harness <name>`, `--assignee <name>`, `--status <state>`,
`--ticket <id>`, `--title <text>`, `--requirement <text>`, `--manual`,
`--dry-run`, `--no-commit`, `--no-confluence`

## Configuration (PRD 2.2)

`~/.config/ai-harness/config.yaml`:

```yaml
core:
  default_harness: "claude-code"   # ollama | lmstudio | claude-code | copilot-cli
  workspace_dir: ".ai-context"

ticketing:
  provider: "jira"                 # jira | azure | manual
  status_filter: "ready-to-work"
  assignee: "dev@company.com"
  jira:
    url: "https://company.atlassian.net"
    username: "dev@company.com"
    # token loaded via env: AI_HARNESS_JIRA_TOKEN / JIRA_API_TOKEN

confluence:
  enabled: true
  space_key: "ENG"
  parent_page_id: "123456789"
  # token loaded via env: AI_HARNESS_CONFLUENCE_TOKEN / CONFLUENCE_API_TOKEN

harnesses:
  claude-code: { command: "claude", args: [] }
  copilot-cli: { command: "gh", args: ["copilot", "suggest"] }
  ollama:      { command: "ollama", args: ["run", "qwen2.5-coder"] }
```

## Storage layout (PRD 2.5)

```text
.ai-context/
├── features/
│   └── PROJ-123.md                 # pulled requirement injected here
├── sessions/
│   └── 2026-10-04_PROJ-123.log     # session start/end log
└── CLAUDE.md                       # harness instruction file
    (or .github/copilot-instructions.md, prompt.md)
```

## Architecture

```text
main.go → cmd/                 CLI dispatcher + start/list/config flows
pkg/config/   Config Manager  viper → struct, PATs via BindEnv (env only)
pkg/ticket/   Ticketing Engine WorkItem + Provider (jira / azure / manual)
pkg/context/  Context Builder  WorkItem → harness markdown files
pkg/runner/   Harness Runner   os/exec with stdio bound to the subprocess
pkg/storage/  Persistence      go-git auto-commit + Confluence REST push
```

Key interfaces (PRD 2.3):

```go
type Provider interface {
    GetActiveWorkItems(assignee string, status string) ([]WorkItem, error)
}

type Harness interface {
    PrepareContext(req ticket.WorkItem, workspaceDir string) error
    Execute(ctx context.Context) error
}
```

## Security notes

- PATs come **only** from environment variables (`viper.BindEnv`); the config
  example file is written with `0600` permissions.
- Work-item IDs are sanitized before use as file names (no path traversal).
- Ticket content is HTML-escaped before conversion to Confluence storage
  format (stored-XSS guard).
- No shell is involved in delegation: `exec.CommandContext` with an argv
  slice, so no command-injection surface.
- HTTP clients carry explicit timeouts.

## Development

```sh
go vet ./...
go test -race -cover ./...        # unit + end-to-end tests
go test -bench=. -benchmem ./...  # performance benchmarks
gosec ./...                       # security scanner
govulncheck ./...                 # vulnerability check
```

## Out of scope (delegated to the harness)

OAuth flows, LLM API token management, token chunking, direct LLM REST calls.
