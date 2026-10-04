// Package cmd implements the setu CLI. The dispatcher is intentionally
// dependency-free: subcommands are plain subcommands of os.Args (PRD 1.2:
// "setu start").
package cmd

import (
	"fmt"
	"io"
)

const usage = `setu - AI Harness Orchestrator CLI (delegator pattern)

Usage:
  setu start    [flags]   Resolve ticket, prepare context, delegate to harness
  setu list     [flags]   List available work items without starting a session
  setu config init        Write an example config to ~/.config/ai-harness/config.yaml
  setu version           Print version

Flags (start/list):
  --config <path>      Config file (default ~/.config/ai-harness/config.yaml)
  --harness <name>     Override default harness (claude-code|copilot-cli|ollama|lmstudio)
  --assignee <name>    Assignee to query (default from config)
  --status <state>     Status filter (default ready-to-work)
  --ticket <id>        Skip the menu; use this work item ID from the provider
  --title <text>       With --manual: work item title
  --requirement <text> With --manual: work item requirement body
  --manual             Use the manual provider (offline)
  --dry-run            Prepare context files only; do not execute the harness
  --no-commit          Skip the git commit during persistence
  --no-confluence      Skip the Confluence push during persistence

Environment:
  AI_HARNESS_JIRA_TOKEN, JIRA_API_TOKEN, AI_HARNESS_AZURE_PAT,
  AZURE_DEVOPS_PAT, AI_HARNESS_CONFLUENCE_TOKEN, CONFLUENCE_API_TOKEN
`

// Execute dispatches CLI args (excluding argv[0]) and returns a process exit code.
func Execute(args []string, in io.Reader, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errOut, usage)
		return 2
	}

	switch args[0] {
	case "start":
		return runStart(args[1:], in, out, errOut)
	case "list":
		return runList(args[1:], out, errOut)
	case "config":
		if len(args) > 1 && args[1] == "init" {
			return runConfigInit(args[2:], out, errOut)
		}
		fmt.Fprintln(errOut, "usage: setu config init")
		return 2
	case "version":
		fmt.Fprintln(out, Version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return 0
	default:
		fmt.Fprintf(errOut, "setu: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
