// Package storage implements the Persistence Engine (PRD section 2.1/2.5).
// After the delegated session ends, it commits the workspace directory to the
// current git branch (via go-git) and optionally pushes a Markdown summary to
// the Confluence REST API.
package storage

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// GitCommitter commits workspace changes in the repository that contains dir.
type GitCommitter struct {
	AuthorName  string
	AuthorEmail string
	Now         func() time.Time // injectable for tests
}

// NewGitCommitter builds a committer with defaults from the environment.
func NewGitCommitter(name, email string) *GitCommitter {
	if name == "" {
		name = os.Getenv("GIT_AUTHOR_NAME")
	}
	if name == "" {
		name = "ai-harness"
	}
	if email == "" {
		email = os.Getenv("GIT_AUTHOR_EMAIL")
	}
	if email == "" {
		email = "ai-harness@localhost"
	}
	return &GitCommitter{AuthorName: name, AuthorEmail: email}
}

func (g *GitCommitter) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// under reports whether name is the workspace dir or lives inside it.
func under(name, prefix string) bool {
	return name == prefix || strings.HasPrefix(name, prefix+"/")
}

func sortEntries(entries []*index.Entry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
}

// Commit stages the workspace directory only: the commit tree is built from
// HEAD's entries outside the workspace plus the freshly staged workspace
// entries. Anything else the developer staged (e.g. unrelated files) is never
// swept into this commit and remains staged exactly as it was afterwards.
//
// Returns the commit hash hex string.
func (g *GitCommitter) Commit(path string, message string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("storage: path %s: %w", path, err)
	}

	repo, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return "", fmt.Errorf("storage: open repository: %w", err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("storage: worktree: %w", err)
	}
	rel := relativeToWorktree(worktree, path)

	// 1. Snapshot the developer's current index (their staged state) so it
	//    can be restored untouched after the scoped commit.
	oldIdx, err := repo.Storer.Index()
	if err != nil {
		return "", fmt.Errorf("storage: read index: %w", err)
	}
	oldEntries := make([]*index.Entry, len(oldIdx.Entries))
	for i, e := range oldIdx.Entries {
		cp := *e
		oldEntries[i] = &cp
	}
	restoreOld := func() {
		cp := make([]*index.Entry, len(oldEntries))
		copy(cp, oldEntries)
		_ = repo.Storer.SetIndex(&index.Index{Version: oldIdx.Version, Entries: cp})
	}

	// 2. Stage the workspace directory (go-git persists this to the index).
	if _, err := worktree.Add(rel); err != nil {
		restoreOld()
		return "", fmt.Errorf("storage: git add %s: %w", rel, err)
	}

	curIdx, err := repo.Storer.Index()
	if err != nil {
		restoreOld()
		return "", fmt.Errorf("storage: read index after add: %w", err)
	}

	// Workspace entries: everything under rel, first stage only.
	wsEntries := make([]*index.Entry, 0, len(curIdx.Entries))
	for _, e := range curIdx.Entries {
		if e.Stage == 0 && under(e.Name, rel) {
			cp := *e
			wsEntries = append(wsEntries, &cp)
		}
	}

	// 3. Load HEAD's tree (nil for an unborn branch / first commit).
	var headTree *object.Tree
	if head, err := repo.Head(); err == nil {
		if commit, err := repo.CommitObject(head.Hash()); err == nil {
			if tree, err := commit.Tree(); err == nil {
				headTree = tree
			}
		}
	}

	// 4. Detect real changes against HEAD: a new/different workspace blob,
	//    or a workspace file present in HEAD but gone from the index
	//    (go-git's Add stages deletions by removing the entry).
	changed := false
	if headTree == nil {
		changed = len(wsEntries) > 0
	} else {
		wsSet := make(map[string]plumbing.Hash, len(wsEntries))
		for _, e := range wsEntries {
			wsSet[e.Name] = e.Hash
			fe, err := headTree.FindEntry(e.Name)
			if err != nil || fe.Hash != e.Hash {
				changed = true
			}
		}
		if !changed {
			flattenTree(headTree, "", "", func(e *index.Entry) {
				if under(e.Name, rel) {
					if _, ok := wsSet[e.Name]; !ok {
						changed = true
					}
				}
			})
		}
	}
	if !changed {
		restoreOld()
		return "", fmt.Errorf("storage: nothing to commit under %s", rel)
	}

	// 5. Build the commit index: HEAD content outside the workspace (keeps
	//    the rest of the tree intact with no diff) + workspace entries.
	commitEntries := make([]*index.Entry, 0, len(wsEntries)+len(oldEntries))
	commitEntries = append(commitEntries, wsEntries...)
	if headTree != nil {
		flattenTree(headTree, "", rel, func(e *index.Entry) {
			commitEntries = append(commitEntries, e)
		})
	}
	sortEntries(commitEntries)
	commitIdx := &index.Index{Version: oldIdx.Version, Entries: commitEntries}
	if err := repo.Storer.SetIndex(commitIdx); err != nil {
		restoreOld()
		return "", fmt.Errorf("storage: set scoped index: %w", err)
	}

	// 6. Create the commit from the scoped index.
	if message == "" {
		message = fmt.Sprintf("setu: persist session context (%s)", g.now().Format(time.RFC3339))
	}
	hash, err := worktree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  g.AuthorName,
			Email: g.AuthorEmail,
			When:  g.now(),
		},
	})
	if err != nil {
		restoreOld()
		return "", fmt.Errorf("storage: commit: %w", err)
	}

	// 7. Restore the developer's staged state: their entries outside the
	//    workspace (including merge-conflict stages 1/2/3, which must stay
	//    resolvable), plus workspace entries now matching the new HEAD.
	restored := make([]*index.Entry, 0, len(oldEntries))
	for _, e := range oldEntries {
		if !under(e.Name, rel) {
			restored = append(restored, e)
		}
	}
	restored = append(restored, wsEntries...)
	sortEntries(restored)
	if err := repo.Storer.SetIndex(&index.Index{Version: oldIdx.Version, Entries: restored}); err != nil {
		return "", fmt.Errorf("storage: restore index: %w", err)
	}

	return hash.String(), nil
}

// flattenTree walks a tree recursively, yielding file entries with full repo
// relative paths, skipping the workspace prefix entirely.
func flattenTree(t *object.Tree, treePrefix, skip string, fn func(*index.Entry)) {
	for _, e := range t.Entries {
		full := e.Name
		if treePrefix != "" {
			full = treePrefix + "/" + e.Name
		}
		if under(full, skip) {
			continue
		}
		if e.Mode == filemode.Dir {
			sub, err := t.Tree(e.Name)
			if err != nil {
				continue
			}
			flattenTree(sub, full, skip, fn)
			continue
		}
		entry := &index.Entry{Name: full, Hash: e.Hash, Mode: e.Mode}
		fn(entry)
	}
}

// relativeToWorktree converts an absolute or nested path into the worktree
// relative form required by go-git's Add.
func relativeToWorktree(wt *git.Worktree, path string) string {
	root := wt.Filesystem.Root()
	if root != "" && strings.HasPrefix(path, root) {
		trimmed := strings.TrimPrefix(path, root)
		return strings.TrimLeft(trimmed, "/\\")
	}
	return path
}
