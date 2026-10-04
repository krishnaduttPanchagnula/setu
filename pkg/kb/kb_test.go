package kb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// fixture builds a workspace with two completed runs (one failed), one
// in-progress run, a feature file and an instruction file.
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	ctx := filepath.Join(root, ".ai-context")
	for _, sub := range []string{"features", "sessions"} {
		if err := os.MkdirAll(filepath.Join(ctx, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	feature := "# PROJ-1 - Add login page\n\n" +
		"## Requirement\n\nUsers must be able to log in with email.\n\n" +
		"## Notes\n\n- Source: https://jira.example/browse/PROJ-1\n- Generated: 2026-10-04T10:00:00Z\n"
	if err := os.WriteFile(filepath.Join(ctx, "features", "PROJ-1.md"), []byte(feature), 0o644); err != nil {
		t.Fatal(err)
	}
	log := "[2026-10-04T10:00:00Z] session start: PROJ-1 (Add login page)\n" +
		"[2026-10-04T10:00:01Z] context files generated\n" +
		"[2026-10-04T10:05:00Z] session end: harness=claude-code duration=5m0s status=ok\n" +
		"[2026-10-04T11:00:00Z] session start: PROJ-1 (Add login page)\n" +
		"[2026-10-04T11:00:02Z] session end: harness=ollama duration=2s status=failed\n" +
		"[2026-10-05T09:00:00Z] session start: PROJ-1 (Add login page)\n"
	if err := os.WriteFile(filepath.Join(ctx, "sessions", "2026-10-04_PROJ-1.log"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ctx, "CLAUDE.md"), []byte("# Active Work Item: PROJ-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDiscoverParsesSessionsAndFeatures(t *testing.T) {
	root := fixture(t)

	k, err := Discover([]string{root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(k.Sessions) != 3 {
		t.Fatalf("sessions = %d, want 3", len(k.Sessions))
	}
	if len(k.Workspaces) != 1 {
		t.Fatalf("workspaces = %d, want 1", len(k.Workspaces))
	}
	if k.Items != 1 {
		t.Errorf("Items = %d, want 1", k.Items)
	}

	// Sorted newest first: the in-progress run (Oct 5) leads.
	first := k.Sessions[0]
	if first.Status != "in-progress" {
		t.Errorf("first status = %q, want in-progress", first.Status)
	}
	if first.Harness != "" {
		t.Errorf("in-progress harness = %q, want empty", first.Harness)
	}

	// Find the failed run.
	var failed *Session
	var ok *Session
	for i := range k.Sessions {
		switch k.Sessions[i].Status {
		case "failed":
			failed = &k.Sessions[i]
		case "completed":
			ok = &k.Sessions[i]
		}
	}
	if failed == nil || ok == nil {
		t.Fatalf("expected one failed and one completed session, got %+v", statuses(k.Sessions))
	}
	if failed.Harness != "ollama" || failed.Duration != "2s" {
		t.Errorf("failed = harness %q duration %q", failed.Harness, failed.Duration)
	}
	if ok.Harness != "claude-code" || ok.Duration != "5m0s" {
		t.Errorf("ok = harness %q duration %q", ok.Harness, ok.Duration)
	}

	// Feature fields are attached to every session of that item.
	if !strings.Contains(ok.Requirement, "log in with email") {
		t.Errorf("requirement = %q", ok.Requirement)
	}
	if ok.SourceURL != "https://jira.example/browse/PROJ-1" {
		t.Errorf("source = %q", ok.SourceURL)
	}
	if ok.Title != "Add login page" || ok.ID != "PROJ-1" {
		t.Errorf("id/title = %q/%q", ok.ID, ok.Title)
	}
	if ok.Date != "2026-10-04" {
		t.Errorf("date = %q", ok.Date)
	}

	// Per-session log lines: first session owns lines 1-3.
	if len(ok.LogLines) != 3 {
		t.Errorf("ok.LogLines = %v, want 3 lines", ok.LogLines)
	}
	if len(failed.LogLines) != 2 {
		t.Errorf("failed.LogLines = %v, want 2 lines", failed.LogLines)
	}
	if len(first.LogLines) != 1 {
		t.Errorf("in-progress LogLines = %v, want 1 line", first.LogLines)
	}

	ws := k.Workspaces[0]
	if ws.Sessions != 3 || ws.Items != 1 {
		t.Errorf("workspace counts = %d sessions, %d items", ws.Sessions, ws.Items)
	}
	if len(ws.Files) != 1 || !strings.HasSuffix(ws.Files[0].Path, "CLAUDE.md") {
		t.Errorf("files = %+v", ws.Files)
	} else if !strings.Contains(ws.Files[0].Content, "PROJ-1") {
		t.Errorf("file content = %q", ws.Files[0].Content)
	}
}

func statuses(sessions []Session) []string {
	var out []string
	for _, s := range sessions {
		out = append(out, s.Status)
	}
	return out
}

func TestDiscoverSkipsHiddenAndVendorDirs(t *testing.T) {
	root := t.TempDir()
	// A decoy inside .git and node_modules must not be discovered.
	for _, hidden := range []string{".git", "node_modules", "vendor"} {
		ctx := filepath.Join(root, hidden, ".ai-context", "sessions")
		if err := os.MkdirAll(ctx, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ctx, "2026-10-04_X-1.log"),
			[]byte("[2026-10-04T10:00:00Z] session start: X-1 (t)\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The real one lives at a modest depth.
	real := filepath.Join(root, "services", "web", ".ai-context", "sessions")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "2026-10-04_Y-1.log"),
		[]byte("[2026-10-04T10:00:00Z] session start: Y-1 (t)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	k, err := Discover([]string{root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(k.Sessions) != 1 || k.Sessions[0].ID != "Y-1" {
		t.Fatalf("sessions = %+v, want only Y-1", k.Sessions)
	}
}

func TestDiscoverMissingRootIsNotAnError(t *testing.T) {
	k, err := Discover([]string{filepath.Join(t.TempDir(), "does-not-exist")})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(k.Sessions) != 0 || len(k.Workspaces) != 0 {
		t.Errorf("expected empty KB, got %+v", k)
	}
}

func TestDiscoverDeduplicatesRoots(t *testing.T) {
	root := fixture(t)
	k, err := Discover([]string{root, root, filepath.Join(root, ".ai-context")})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(k.Workspaces) != 1 {
		t.Errorf("workspaces = %d, want 1 (deduplicated)", len(k.Workspaces))
	}
}

func TestDiscoverReportsGitCommits(t *testing.T) {
	root := fixture(t)
	repo, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add(".ai-context"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	_, err = wt.Commit("setu: persist PROJ-1 (Add login page)", &git.CommitOptions{
		Author: &object.Signature{Name: "Tester", Email: "t@example.com", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	// An unrelated commit must not be reported.
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("docs: readme", &git.CommitOptions{
		Author: &object.Signature{Name: "Tester", Email: "t@example.com", When: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}

	k, err := Discover([]string{root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	commits := k.Workspaces[0].Commits
	if len(commits) != 1 {
		t.Fatalf("commits = %+v, want only the .ai-context one", commits)
	}
	if !strings.HasPrefix(commits[0].Message, "setu: persist PROJ-1") {
		t.Errorf("message = %q", commits[0].Message)
	}
	if len(commits[0].Hash) != 8 || commits[0].Author != "Tester" {
		t.Errorf("commit = %+v", commits[0])
	}
}

func TestParseFeatureRejectsMalformedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.md")
	if err := os.WriteFile(path, []byte("no heading here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := parseFeature(path); ok {
		t.Error("expected malformed feature file to be rejected")
	}
}

func TestCapContentTruncates(t *testing.T) {
	big := strings.Repeat("x", 600*1024)
	out := capContent(big)
	if len(out) >= len(big) {
		t.Errorf("expected truncation, got %d bytes", len(out))
	}
	if !strings.HasSuffix(out, "(truncated)") {
		t.Error("expected truncation marker")
	}
}

func BenchmarkDiscover(b *testing.B) {
	// Reuse the fixture logic without *testing.T.
	root := b.TempDir()
	ctx := filepath.Join(root, ".ai-context")
	_ = os.MkdirAll(filepath.Join(ctx, "features"), 0o755)
	_ = os.MkdirAll(filepath.Join(ctx, "sessions"), 0o755)
	_ = os.WriteFile(filepath.Join(ctx, "features", "PROJ-1.md"),
		[]byte("# PROJ-1 - T\n\n## Requirement\n\nR\n"), 0o644)
	_ = os.WriteFile(filepath.Join(ctx, "sessions", "2026-10-04_PROJ-1.log"),
		[]byte("[2026-10-04T10:00:00Z] session start: PROJ-1 (T)\n"+
			"[2026-10-04T10:00:01Z] session end: harness=claude-code duration=1s status=ok\n"), 0o644)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Discover([]string{root}); err != nil {
			b.Fatal(err)
		}
	}
}
