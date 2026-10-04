package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"setu/pkg/ticket"
)

func fixedItem() ticket.WorkItem {
	return ticket.WorkItem{
		ID:          "PROJ-123",
		Title:       "Implement auth flow",
		Requirement: "Users must authenticate with a PAT.",
		URL:         "https://company.atlassian.net/browse/PROJ-123",
	}
}

func fixedNow() time.Time {
	return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
}

// TestBuildAllLayout asserts the PRD section 2.5 directory structure.
func TestBuildAllLayout(t *testing.T) {
	t.Chdir(t.TempDir())
	b := NewBuilder(".ai-context")
	b.Now = fixedNow

	artifacts, err := b.BuildAll(fixedItem(), "claude-code")
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}

	wantFiles := []string{
		filepath.Join(".ai-context", "features", "PROJ-123.md"),
		filepath.Join(".ai-context", "CLAUDE.md"),
		filepath.Join(".ai-context", "sessions", "2026-10-04_PROJ-123.log"),
	}
	for _, f := range wantFiles {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("expected file missing: %s (%v)", f, err)
		}
	}
	if artifacts.FeatureFile != wantFiles[0] {
		t.Errorf("FeatureFile = %q, want %q", artifacts.FeatureFile, wantFiles[0])
	}
	if artifacts.InstructionFile != wantFiles[1] {
		t.Errorf("InstructionFile = %q, want %q", artifacts.InstructionFile, wantFiles[1])
	}
	if artifacts.SessionLog != wantFiles[2] {
		t.Errorf("SessionLog = %q, want %q", artifacts.SessionLog, wantFiles[2])
	}

	feature, err := os.ReadFile(wantFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	content := string(feature)
	for _, want := range []string{"PROJ-123", "Implement auth flow", "PAT", "https://company.atlassian.net/browse/PROJ-123"} {
		if !strings.Contains(content, want) {
			t.Errorf("feature file missing %q", want)
		}
	}
}

// TestInstructionFilePerHarness verifies PRD section 1.2 step 4 targeting.
func TestInstructionFilePerHarness(t *testing.T) {
	tests := []struct {
		harness string
		want    string
	}{
		{"claude-code", filepath.Join(".ai-context", "CLAUDE.md")},
		{"copilot-cli", filepath.Join(".github", "copilot-instructions.md")},
		{"ollama", filepath.Join(".ai-context", "prompt.md")},
	}
	for _, tc := range tests {
		t.Run(tc.harness, func(t *testing.T) {
			t.Chdir(t.TempDir())
			b := NewBuilder(".ai-context")
			b.Now = fixedNow

			artifacts, err := b.BuildAll(fixedItem(), tc.harness)
			if err != nil {
				t.Fatalf("BuildAll: %v", err)
			}
			if artifacts.InstructionFile != tc.want {
				t.Errorf("instruction file = %q, want %q", artifacts.InstructionFile, tc.want)
			}
			body, err := os.ReadFile(tc.want)
			if err != nil {
				t.Fatalf("instruction file missing: %v", err)
			}
			if !strings.Contains(string(body), "PROJ-123") {
				t.Error("instruction file does not reference the work item")
			}
		})
	}
}

// TestSessionLogAppends ensures repeated sessions append rather than truncate.
func TestSessionLogAppends(t *testing.T) {
	t.Chdir(t.TempDir())
	b := NewBuilder(".ai-context")
	b.Now = fixedNow

	item := fixedItem()
	if err := b.BuildSessionLog(item, nil); err != nil {
		t.Fatal(err)
	}
	if err := b.AppendSessionNote(item, "session end: duration=1s"); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(".ai-context", "sessions", "2026-10-04_PROJ-123.log")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if c := strings.Count(string(raw), "session start"); c != 1 {
		t.Errorf("session start count = %d, want 1", c)
	}
	if !strings.Contains(string(raw), "duration=1s") {
		t.Error("end note missing")
	}
}

// TestBuildAllRejectsInvalidItem ensures validation runs before file writes.
func TestBuildAllRejectsInvalidItem(t *testing.T) {
	t.Chdir(t.TempDir())
	b := NewBuilder(".ai-context")

	if _, err := b.BuildAll(ticket.WorkItem{Title: "no id"}, "claude-code"); err == nil {
		t.Fatal("expected validation error")
	}
	if _, err := os.Stat(filepath.Join(".ai-context", "features")); !os.IsNotExist(err) {
		t.Error("no files should be written for an invalid item")
	}
}

// TestSanitizeIDBlocksPathTraversal ensures crafted IDs cannot escape the
// workspace directory (security: path traversal).
func TestSanitizeIDBlocksPathTraversal(t *testing.T) {
	t.Chdir(t.TempDir())
	b := NewBuilder(".ai-context")
	b.Now = fixedNow

	item := fixedItem()
	item.ID = "../../etc/passwd"

	artifacts, err := b.BuildAll(item, "claude-code")
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}

	// The artifact must live inside the workspace features directory.
	abs, err := filepath.Abs(artifacts.FeatureFile)
	if err != nil {
		t.Fatal(err)
	}
	features, err := filepath.Abs(filepath.Join(".ai-context", "features"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(abs, features) {
		t.Errorf("feature file escaped workspace: %s", abs)
	}
	if strings.Contains(filepath.Base(artifacts.FeatureFile), "..") {
		t.Errorf("sanitized file name contains traversal: %q", artifacts.FeatureFile)
	}
	if strings.Contains(artifacts.FeatureFile, "/../") || strings.HasSuffix(artifacts.FeatureFile, "..") {
		t.Errorf("sanitized path contains traversal segments: %q", artifacts.FeatureFile)
	}
}
