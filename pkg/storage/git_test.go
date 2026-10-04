package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/index"
)

// newTestRepo creates a fresh git repository and chdirs into it.
func newTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := git.PlainInit(dir, false); err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	t.Chdir(dir)
	return dir
}

// TestCommitWorkspace verifies the persistence engine stages and commits
// the .ai-context directory (PRD section 2.5).
func TestCommitWorkspace(t *testing.T) {
	dir := newTestRepo(t)

	ws := filepath.Join(dir, ".ai-context")
	if err := os.MkdirAll(filepath.Join(ws, "features"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(ws, "features", "PROJ-1.md"),
		[]byte("# PROJ-1\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	g := NewGitCommitter("Tester", "tester@example.com")
	g.Now = func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }

	hash, err := g.Commit(".ai-context", "setu: persist PROJ-1")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(hash) != 40 {
		t.Errorf("hash = %q, want 40-char sha1", hash)
	}

	// Verify the commit exists and contains the workspace file.
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatal(err)
	}
	if commit.Message != "setu: persist PROJ-1" {
		t.Errorf("commit message = %q", commit.Message)
	}
	if commit.Author.Name != "Tester" || commit.Author.Email != "tester@example.com" {
		t.Errorf("author = %s <%s>", commit.Author.Name, commit.Author.Email)
	}

	tree, err := commit.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.File(".ai-context/features/PROJ-1.md"); err != nil {
		t.Errorf("committed tree missing workspace file: %v", err)
	}
}

// TestCommitNothingToCommit ensures a clean tree reports a clear error.
func TestCommitNothingToCommit(t *testing.T) {
	newTestRepo(t)
	ws := ".ai-context"
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}

	g := NewGitCommitter("", "")
	if _, err := g.Commit(ws, "should fail"); err == nil {
		t.Fatal("expected 'nothing to commit' error on clean tree")
	}
}

// TestCommitMissingPath validates input before touching the repository.
func TestCommitMissingPath(t *testing.T) {
	newTestRepo(t)
	g := NewGitCommitter("", "")
	if _, err := g.Commit("does-not-exist", "msg"); err == nil {
		t.Fatal("expected error for missing path")
	}
}

// TestCommitOutsideRepo reports ungit'd directories cleanly.
func TestCommitOutsideRepo(t *testing.T) {
	t.Chdir(t.TempDir()) // no .git anywhere up the tree in tmp
	ws := ".ai-context"
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "x.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := NewGitCommitter("", "")
	if _, err := g.Commit(ws, "msg"); err == nil {
		t.Fatal("expected error when not inside a git repository")
	}
}

// TestCommitExcludesFilesOutsideWorkspace guards against sweeping the
// developer's unrelated staged work (e.g. secrets) into the session commit,
// while preserving their staged state afterwards.
func TestCommitExcludesFilesOutsideWorkspace(t *testing.T) {
	dir := newTestRepo(t)
	g := NewGitCommitter("Tester", "tester@example.com")

	// 1. Tracked file committed first, so HEAD has outside content.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# repo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit("README.md", "initial"); err != nil {
		t.Fatalf("initial commit: %v", err)
	}

	// 2. Developer stages an unrelated file that must NOT be committed.
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("hunter2"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("secret.txt"); err != nil {
		t.Fatalf("user add: %v", err)
	}

	// 3. Session workspace.
	ws := filepath.Join(dir, ".ai-context", "features")
	if err := os.MkdirAll(ws, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "PROJ-1.md"), []byte("# PROJ-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 4. Scoped commit.
	hash, err := g.Commit(".ai-context", "setu: persist PROJ-1")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	commit, err := repo.CommitObject(plumbing.NewHash(hash))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := commit.Tree()
	if err != nil {
		t.Fatal(err)
	}

	// Workspace committed.
	if _, err := tree.File(".ai-context/features/PROJ-1.md"); err != nil {
		t.Errorf("workspace file missing from commit: %v", err)
	}
	// Outside content preserved from HEAD (no deletion diff).
	if _, err := tree.File("README.md"); err != nil {
		t.Errorf("README.md dropped from commit: %v", err)
	}
	// User's staged secret excluded.
	if _, err := tree.File("secret.txt"); err == nil {
		t.Error("secret.txt was swept into the session commit")
	}

	// 5. User's staged state preserved after the commit.
	idx, err := repo.Storer.Index()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range idx.Entries {
		if e.Name == "secret.txt" {
			found = true
		}
	}
	if !found {
		t.Error("developer's staged file was dropped from the index")
	}
}

// TestCommitNoChangesOnRerun ensures a second identical session commit
// reports 'nothing to commit' instead of creating an empty commit.
func TestCommitNoChangesOnRerun(t *testing.T) {
	dir := newTestRepo(t)
	g := NewGitCommitter("T", "t@example.com")

	ws := filepath.Join(dir, ".ai-context")
	if err := os.MkdirAll(ws, 0o750); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(ws, "x.md")
	if err := os.WriteFile(f, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(ws, "first"); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	if _, err := g.Commit(ws, "second"); err == nil {
		t.Fatal("expected 'nothing to commit' when workspace is unchanged")
	}
	// Change the file: a new commit must succeed.
	if err := os.WriteFile(f, []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(ws, "third"); err != nil {
		t.Fatalf("third commit: %v", err)
	}
}

// TestCommitPersistsWorkspaceDeletion covers the deletion-only change: a
// session where the only delta is a removed workspace file must commit the
// removal (go-git's Add stages deletions by dropping the index entry).
func TestCommitPersistsWorkspaceDeletion(t *testing.T) {
	dir := newTestRepo(t)
	g := NewGitCommitter("T", "t@example.com")

	ws := filepath.Join(dir, ".ai-context")
	if err := os.MkdirAll(ws, 0o750); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(ws, "keep.md")
	gone := filepath.Join(ws, "gone.md")
	if err := os.WriteFile(keep, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gone, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(ws, "first"); err != nil {
		t.Fatalf("first commit: %v", err)
	}

	// The only change: delete one workspace file.
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	hash, err := g.Commit(ws, "second")
	if err != nil {
		t.Fatalf("deletion commit: %v", err)
	}

	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.CommitObject(plumbing.NewHash(hash))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := commit.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.File(".ai-context/gone.md"); err == nil {
		t.Error("deleted workspace file still present in commit")
	}
	if _, err := tree.File(".ai-context/keep.md"); err != nil {
		t.Errorf("surviving workspace file missing: %v", err)
	}
}

// TestCommitPreservesMergeConflictIndex ensures the scoped commit never
// destroys a developer's in-progress merge: stage 1/2/3 entries for files
// outside the workspace must survive the session commit.
func TestCommitPreservesMergeConflictIndex(t *testing.T) {
	dir := newTestRepo(t)
	g := NewGitCommitter("T", "t@example.com")

	// Initial commit so HEAD and a real blob hash exist.
	base := filepath.Join(dir, "base.txt")
	if err := os.WriteFile(base, []byte("base content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit("base.txt", "initial"); err != nil {
		t.Fatalf("initial commit: %v", err)
	}

	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := repo.Storer.Index()
	if err != nil {
		t.Fatal(err)
	}
	var blob plumbing.Hash
	for _, e := range idx.Entries {
		if e.Name == "base.txt" {
			blob = e.Hash
		}
	}
	if blob.IsZero() {
		t.Fatal("base.txt not in index")
	}

	// Fabricate an unresolved merge conflict for an outside file: stages
	// 1/2/3 with no stage-0 entry (git's representation of conflicts).
	conflictEntries := []*index.Entry{}
	for stage := index.Stage(1); stage <= 3; stage++ {
		conflictEntries = append(conflictEntries, &index.Entry{
			Name:  "conflict.txt",
			Hash:  blob,
			Mode:  filemode.Regular,
			Stage: stage,
		})
	}
	idx.Entries = append(idx.Entries, conflictEntries...)
	if err := repo.Storer.SetIndex(idx); err != nil {
		t.Fatal(err)
	}

	// Session workspace + scoped commit.
	ws := filepath.Join(dir, ".ai-context")
	if err := os.MkdirAll(ws, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "s.md"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(ws, "session commit"); err != nil {
		t.Fatalf("session commit: %v", err)
	}

	// Conflict state must still be resolvable afterwards.
	after, err := repo.Storer.Index()
	if err != nil {
		t.Fatal(err)
	}
	stages := map[index.Stage]bool{}
	for _, e := range after.Entries {
		if e.Name == "conflict.txt" {
			stages[e.Stage] = true
		}
	}
	for s := index.Stage(1); s <= 3; s++ {
		if !stages[s] {
			t.Errorf("merge conflict stage %d was dropped from the index", s)
		}
	}
}

// TestCommitPreservesNestedHeadContent exercises flattenTree recursion:
// HEAD content in nested directories outside the workspace must be carried
// into every session commit (no accidental deletions).
func TestCommitPreservesNestedHeadContent(t *testing.T) {
	dir := newTestRepo(t)
	g := NewGitCommitter("T", "t@example.com")

	// Nested outside content committed first.
	nested := filepath.Join(dir, "src", "deep")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "app.go"), []byte("package deep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit("src", "src commit"); err != nil {
		t.Fatalf("src commit: %v", err)
	}

	// Two successive workspace commits.
	ws := filepath.Join(dir, ".ai-context")
	if err := os.MkdirAll(ws, 0o750); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(ws, "a.md")
	if err := os.WriteFile(f, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(ws, "first session"); err != nil {
		t.Fatalf("first session: %v", err)
	}
	if err := os.WriteFile(f, []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := g.Commit(ws, "second session")
	if err != nil {
		t.Fatalf("second session: %v", err)
	}

	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.CommitObject(plumbing.NewHash(hash))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := commit.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.File("src/deep/app.go"); err != nil {
		t.Errorf("nested HEAD content lost after session commit: %v", err)
	}
	if _, err := tree.File(".ai-context/a.md"); err != nil {
		t.Errorf("workspace file missing: %v", err)
	}
	raw, err := tree.File(".ai-context/a.md")
	if err != nil {
		t.Fatal(err)
	}
	content, err := raw.Contents()
	if err != nil {
		t.Fatal(err)
	}
	if content != "v2" {
		t.Errorf("workspace content = %q, want v2", content)
	}
}

// TestDefaultCommitterIdentity ensures sane fallbacks for author name/email.
func TestDefaultCommitterIdentity(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "")
	t.Setenv("GIT_AUTHOR_EMAIL", "")
	g := NewGitCommitter("", "")
	if g.AuthorName != "ai-harness" {
		t.Errorf("AuthorName = %q", g.AuthorName)
	}
	if g.AuthorEmail != "ai-harness@localhost" {
		t.Errorf("AuthorEmail = %q", g.AuthorEmail)
	}
}
