// Package context implements the Context Builder (PRD section 2.1/2.5).
// It translates a WorkItem into harness-specific markdown inside the
// workspace directory (.ai-context) following the documented layout:
//
//	.ai-context/
//	├── features/{ID}.md
//	├── sessions/{date}_{ID}.log
//	└── CLAUDE.md (or copilot/ollama instruction files)
package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"setu/pkg/ticket"
)

// SessionArtifacts records every file produced by a single build pass.
type SessionArtifacts struct {
	FeatureFile     string
	InstructionFile string
	SessionLog      string
}

// Builder writes harness context files relative to the repository root.
type Builder struct {
	// WorkspaceDir is the local state directory, e.g. ".ai-context".
	WorkspaceDir string
	// Now is injectable for deterministic tests; nil means time.Now.
	Now func() time.Time
}

// NewBuilder creates a builder rooted at workspaceDir.
func NewBuilder(workspaceDir string) *Builder {
	if workspaceDir == "" {
		workspaceDir = ".ai-context"
	}
	return &Builder{WorkspaceDir: workspaceDir}
}

func (b *Builder) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// BuildAll produces the feature file, the harness instruction file and the
// session log marker for the given work item.
func (b *Builder) BuildAll(item ticket.WorkItem, harness string) (SessionArtifacts, error) {
	if err := item.Validate(); err != nil {
		return SessionArtifacts{}, err
	}

	artifacts := SessionArtifacts{}
	if err := b.BuildFeatureFile(item, &artifacts.FeatureFile); err != nil {
		return artifacts, err
	}
	if err := b.BuildInstructionFile(item, harness, &artifacts.InstructionFile); err != nil {
		return artifacts, err
	}
	if err := b.BuildSessionLog(item, &artifacts.SessionLog); err != nil {
		return artifacts, err
	}
	return artifacts, nil
}

// BuildFeatureFile writes .ai-context/features/{ID}.md with the requirement.
func (b *Builder) BuildFeatureFile(item ticket.WorkItem, out *string) error {
	dir := filepath.Join(b.WorkspaceDir, "features")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("context: mkdir %s: %w", dir, err)
	}

	id := sanitizeID(item.ID)
	path := filepath.Join(dir, id+".md")
	content := fmt.Sprintf(
		"# %s - %s\n\n"+
			"## Requirement\n\n%s\n\n"+
			"## Notes\n\n- Source: %s\n- Generated: %s\n",
		item.ID, item.Title, item.Requirement, item.URL, b.now().Format(time.RFC3339),
	)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("context: write feature file: %w", err)
	}
	if out != nil {
		*out = path
	}
	return nil
}

// BuildInstructionFile writes the harness-specific instruction file. Claude
// reads {workspace}/CLAUDE.md, Copilot reads .github/copilot-instructions.md,
// other harnesses get a prompt.md in the workspace.
func (b *Builder) BuildInstructionFile(item ticket.WorkItem, harness string, out *string) error {
	featurePath := filepath.Join(b.WorkspaceDir, "features", sanitizeID(item.ID)+".md")
	body := fmt.Sprintf(
		"# Active Work Item: %s\n\n%s\n\n"+
			"Read the full requirement at `%s` before making changes.\n\n"+
			"## Guidelines\n\n- Keep edits scoped to this work item.\n"+
			"- Follow existing repository conventions.\n"+
			"- Summarize what changed when the session ends.\n",
		item.ID, item.Title, filepath.ToSlash(featurePath),
	)

	var path string
	switch strings.ToLower(harness) {
	case "claude-code", "claude":
		path = filepath.Join(b.WorkspaceDir, "CLAUDE.md")
	case "copilot-cli", "copilot":
		if err := os.MkdirAll(".github", 0o750); err != nil {
			return fmt.Errorf("context: mkdir .github: %w", err)
		}
		path = filepath.Join(".github", "copilot-instructions.md")
	default:
		path = filepath.Join(b.WorkspaceDir, "prompt.md")
	}

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("context: write instruction file: %w", err)
	}
	if out != nil {
		*out = path
	}
	return nil
}

// BuildSessionLog appends a start marker to sessions/{date}_{ID}.log.
func (b *Builder) BuildSessionLog(item ticket.WorkItem, out *string) error {
	dir := filepath.Join(b.WorkspaceDir, "sessions")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("context: mkdir %s: %w", dir, err)
	}
	now := b.now()
	name := fmt.Sprintf("%s_%s.log", now.Format("2006-01-02"), sanitizeID(item.ID))
	path := filepath.Join(dir, name)

	line := fmt.Sprintf("[%s] session start: %s (%s)\n", now.Format(time.RFC3339), item.ID, item.Title)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- path built from sanitizeID(item.ID); covered by TestSanitizeIDBlocksPathTraversal
	if err != nil {
		return fmt.Errorf("context: open session log: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		return fmt.Errorf("context: write session log: %w", err)
	}
	if out != nil {
		*out = path
	}
	return nil
}

// AppendSessionNote appends an arbitrary line to the session log for the item,
// used after the harness exits (persistence phase).
func (b *Builder) AppendSessionNote(item ticket.WorkItem, note string) error {
	now := b.now()
	name := fmt.Sprintf("%s_%s.log", now.Format("2006-01-02"), sanitizeID(item.ID))
	path := filepath.Join(b.WorkspaceDir, "sessions", name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- path built from sanitizeID(item.ID); covered by TestSanitizeIDBlocksPathTraversal
	if err != nil {
		return fmt.Errorf("context: open session log: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(fmt.Sprintf("[%s] %s\n", now.Format(time.RFC3339), note)); err != nil {
		return fmt.Errorf("context: append note: %w", err)
	}
	return nil
}

// sanitizeID strips path separators and spaces so IDs cannot escape the
// workspace directory (path traversal defense).
func sanitizeID(id string) string {
	id = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_' || r == '.':
			return r
		default:
			return '-'
		}
	}, id)
	return strings.Trim(id, "-.")
}
