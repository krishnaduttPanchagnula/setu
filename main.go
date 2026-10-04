// Command setu is the AI Harness Orchestrator CLI (PRD section 1).
// It resolves ticket context, templates the workspace, and shells out to the
// native AI harness, which owns authentication and session loops.
package main

import (
	"os"

	"setu/cmd"
)

func main() {
	os.Exit(cmd.Execute(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
