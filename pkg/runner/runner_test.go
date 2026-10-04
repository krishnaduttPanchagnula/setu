package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"setu/pkg/config"
	aicontx "setu/pkg/context"
	"setu/pkg/ticket"
)

// TestPrepareContext verifies the context builder integration (PRD 2.3).
func TestPrepareContext(t *testing.T) {
	t.Chdir(t.TempDir())
	b := aicontx.NewBuilder(".ai-context")

	h, err := NewFromSpec("claude-code", config.HarnessSpec{Command: "echo"}, b)
	if err != nil {
		t.Fatalf("NewFromSpec: %v", err)
	}

	item := ticket.WorkItem{ID: "PROJ-7", Title: "T", Requirement: "R"}
	if err := h.PrepareContext(item, ".ai-context"); err != nil {
		t.Fatalf("PrepareContext: %v", err)
	}
	if _, err := os.Stat(filepath.Join(".ai-context", "features", "PROJ-7.md")); err != nil {
		t.Errorf("feature file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(".ai-context", "CLAUDE.md")); err != nil {
		t.Errorf("CLAUDE.md missing: %v", err)
	}
}

// TestExecuteBindsStdio verifies PRD section 2.4: the child inherits our
// configured stdio sinks and its output lands on the harness stdout.
func TestExecuteBindsStdio(t *testing.T) {
	var out, errBuf bytes.Buffer
	h, err := NewFromSpec("fake", config.HarnessSpec{Command: "echo", Args: []string{"delegated-session"}}, nil)
	if err != nil {
		t.Fatalf("NewFromSpec: %v", err)
	}
	h.Stdin = strings.NewReader("unused")
	h.Stdout = &out
	h.Stderr = &errBuf

	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.TrimSpace(out.String()) != "delegated-session" {
		t.Errorf("stdout = %q, want delegated-session", out.String())
	}
	if errBuf.Len() != 0 {
		t.Errorf("stderr should be empty for this command, got %q", errBuf.String())
	}
}

// TestExecutePropagatesStdin ensures interactive harnesses receive stdin.
func TestExecutePropagatesStdin(t *testing.T) {
	var out bytes.Buffer
	h, err := NewFromSpec("fake", config.HarnessSpec{Command: "cat"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.Stdin = strings.NewReader("hello stdin\n")
	h.Stdout = &out

	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.String() != "hello stdin\n" {
		t.Errorf("stdout = %q", out.String())
	}
}

// TestExecuteFailure surfaces a non-zero exit with context (PRD 2.4).
func TestExecuteFailure(t *testing.T) {
	h, err := NewFromSpec("fake", config.HarnessSpec{Command: "false"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.Stdout = &bytes.Buffer{}
	h.Stderr = &bytes.Buffer{}

	err = h.Execute(context.Background())
	if err == nil {
		t.Fatal("expected error from failing child")
	}
	if !strings.Contains(err.Error(), "exited with code 1") {
		t.Errorf("error = %v, want exit code context", err)
	}
}

// TestExecuteMissingBinary wraps exec failures with the harness name.
func TestExecuteMissingBinary(t *testing.T) {
	h, err := NewFromSpec("ghost", config.HarnessSpec{Command: "definitely-not-a-real-binary-xyz"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.Stdout = &bytes.Buffer{}
	h.Stderr = &bytes.Buffer{}

	err = h.Execute(context.Background())
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should mention harness name: %v", err)
	}
	var execErr *os.PathError
	if !errors.As(err, &execErr) && !strings.Contains(err.Error(), "not found") &&
		!strings.Contains(err.Error(), "no such file") {
		t.Errorf("unexpected error shape: %v", err)
	}
}

// TestExecuteRejectsEmptyCommand enforces the config contract at Execute time.
func TestExecuteRejectsEmptyCommand(t *testing.T) {
	h := &ExecHarness{name: "x"} // bypass NewFromSpec to hit the guard
	if err := h.Execute(context.Background()); err == nil {
		t.Fatal("Execute should reject an empty command")
	}
	if _, err := NewFromSpec("x", config.HarnessSpec{}, nil); err == nil {
		t.Fatal("NewFromSpec should reject an empty command")
	}
}

// TestExecuteTreatsArgsLiterally guards the no-shell security property:
// metacharacters in arguments must never be interpreted (PRD 2.4 delegates
// via argv, never a shell string).
func TestExecuteTreatsArgsLiterally(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pwned")
	payload := "hello; touch " + marker + " && whoami"

	var out bytes.Buffer
	h, err := NewFromSpec("fake", config.HarnessSpec{Command: "echo", Args: []string{payload}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.Stdout = &out
	h.Stderr = &bytes.Buffer{}

	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.String(), "touch "+marker) {
		t.Errorf("argument not passed literally: %q", out.String())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("shell metacharacter was interpreted: marker file was created")
	}
}

// TestExecuteBindsStderr verifies the third stdio binding (PRD 2.4).
func TestExecuteBindsStderr(t *testing.T) {
	var errBuf bytes.Buffer
	h, err := NewFromSpec("fake", config.HarnessSpec{Command: "ls", Args: []string{"/nonexistent-dir-xyz"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.Stdout = &bytes.Buffer{}
	h.Stderr = &errBuf

	execErr := h.Execute(context.Background())
	if execErr == nil {
		t.Fatal("expected a non-zero exit from ls")
	}
	if errBuf.Len() == 0 {
		t.Error("stderr was not bound to the harness output")
	}
}

// TestExecutePassesEnvironment confirms the harness receives an environment.
func TestExecutePassesEnvironment(t *testing.T) {
	var out bytes.Buffer
	h, err := NewFromSpec("fake", config.HarnessSpec{Command: "env"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.Stdout = &out
	h.Stderr = &bytes.Buffer{}
	h.Env = []string{"AI_HARNESS_TEST_MARKER=marker-abc-123"}

	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.String(), "AI_HARNESS_TEST_MARKER=marker-abc-123") {
		t.Errorf("environment not passed through: %q", out.String())
	}
}

// TestExecuteHonorsContextCancel proves cancellation kills the child.
func TestExecuteHonorsContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h, err := NewFromSpec("fake", config.HarnessSpec{Command: "sleep", Args: []string{"30"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.Stdout = &bytes.Buffer{}
	h.Stderr = &bytes.Buffer{}

	if err := h.Execute(ctx); err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// TestCommandEcho returns argv for diagnostics.
func TestCommandEcho(t *testing.T) {
	h, err := NewFromSpec("copilot-cli", config.HarnessSpec{Command: "gh", Args: []string{"copilot", "suggest"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(h.Command(), " ")
	if got != "gh copilot suggest" {
		t.Errorf("Command() = %q", got)
	}
	if h.Name() != "copilot-cli" {
		t.Errorf("Name() = %q", h.Name())
	}
}
