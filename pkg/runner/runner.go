// Package runner implements the Harness Runner (PRD section 2.1, 2.3, 2.4).
// It prepares harness instruction files and delegates the interactive AI
// session to the native tool by binding os.Stdin/os.Stdout/os.Stderr to a
// subprocess. Authentication and token management stay inside the harness.
package runner

import (
	stdcontext "context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"setu/pkg/config"
	aicontx "setu/pkg/context"
	"setu/pkg/ticket"
)

// Harness is the PRD section 2.3 contract.
type Harness interface {
	// PrepareContext writes the WorkItem to the harness-specific file
	// (e.g. CLAUDE.md or .github/copilot-instructions.md).
	PrepareContext(req ticket.WorkItem, workspaceDir string) error

	// Execute hands over os.Stdin/Stdout to the subprocess and blocks until
	// the developer exits the AI session.
	Execute(ctx stdcontext.Context) error
}

// ExecHarness runs a delegated AI command. Create it with NewFromSpec.
type ExecHarness struct {
	name    string
	spec    config.HarnessSpec
	builder *aicontx.Builder

	// I/O sinks default to the process terminal; tests override them.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Env    []string
}

// NewFromSpec creates a harness from a resolved config spec and context
// builder (PRD section 2.2 harness map).
func NewFromSpec(name string, spec config.HarnessSpec, builder *aicontx.Builder) (*ExecHarness, error) {
	if spec.Command == "" {
		return nil, fmt.Errorf("runner: no command configured for harness %q", name)
	}
	return &ExecHarness{
		name:    name,
		spec:    spec,
		builder: builder,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Env:     os.Environ(),
	}, nil
}

// Name returns the harness identifier (claude-code, ollama, ...).
func (h *ExecHarness) Name() string { return h.name }

// Command returns the full argv used for delegation (useful for diagnostics).
func (h *ExecHarness) Command() []string {
	return append([]string{h.spec.Command}, h.spec.Args...)
}

// PrepareContext renders the work item files via the context builder
// (feature file, harness instruction file, session log).
func (h *ExecHarness) PrepareContext(req ticket.WorkItem, workspaceDir string) error {
	if h.builder == nil {
		return fmt.Errorf("runner: no context builder configured")
	}
	if workspaceDir != "" && h.builder.WorkspaceDir != workspaceDir {
		h.builder = aicontx.NewBuilder(workspaceDir)
	}
	_, err := h.builder.BuildAll(req, h.name)
	return err
}

// Execute starts the delegated harness with a direct stdio binding and
// blocks until it exits (PRD section 2.4).
func (h *ExecHarness) Execute(ctx stdcontext.Context) error {
	if h.spec.Command == "" {
		return fmt.Errorf("runner: empty command for harness %q", h.name)
	}

	cmd := exec.CommandContext(ctx, h.spec.Command, h.spec.Args...) // #nosec G204 -- argv comes from the operator's own config file; no shell is involved (delegator pattern, PRD 2.4)

	// Bind terminal I/O directly to the native harness (PRD section 2.4).
	cmd.Stdin = h.Stdin
	cmd.Stdout = h.Stdout
	cmd.Stderr = h.Stderr

	// Pass the environment through so the harness authenticates itself.
	if h.Env != nil {
		cmd.Env = h.Env
	}

	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("runner: harness %q exited with code %d", h.name, ee.ExitCode())
		}
		return fmt.Errorf("runner: harness %q failed: %w", h.name, err)
	}
	return nil
}

// Verify interface compliance at compile time (PRD section 2.3).
var _ Harness = (*ExecHarness)(nil)
