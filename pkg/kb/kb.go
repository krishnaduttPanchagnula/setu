// Package kb scans .ai-context workspaces and turns them into a queryable
// knowledge base of previous runs: session logs, feature requirements,
// instruction files and the git commits that persisted them.
package kb

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
)

// Session is one harness run (a start/end pair inside a session log file).
type Session struct {
	Key         string   `json:"key"`
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Date        string   `json:"date"`
	StartedAt   string   `json:"startedAt"`
	EndedAt     string   `json:"endedAt"`
	Duration    string   `json:"duration"`
	Harness     string   `json:"harness"`
	Status      string   `json:"status"` // completed | failed | in-progress
	LogLines    []string `json:"logLines"`
	LogFile     string   `json:"logFile"`
	Workspace   string   `json:"workspace"`
	ContextDir  string   `json:"contextDir"`
	Requirement string   `json:"requirement"`
	SourceURL   string   `json:"sourceUrl"`
	GeneratedAt string   `json:"generatedAt"`
	FeatureFile string   `json:"featureFile"`
}

// Commit is a git commit that touched the workspace's .ai-context directory.
type Commit struct {
	Hash    string `json:"hash"`
	Message string `json:"message"`
	Author  string `json:"author"`
	When    string `json:"when"`
}

// FileDoc is a context/instruction file with its contents (size-capped).
type FileDoc struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Workspace is one directory containing a .ai-context knowledge store.
type Workspace struct {
	Path       string    `json:"path"`
	ContextDir string    `json:"contextDir"`
	Items      int       `json:"items"`
	Sessions   int       `json:"sessions"`
	Files      []FileDoc `json:"files"`
	Commits    []Commit  `json:"commits"`
}

// KB is the full knowledge base snapshot served to the UI.
type KB struct {
	ScannedAt  string      `json:"scannedAt"`
	Roots      []string    `json:"roots"`
	Sessions   []Session   `json:"sessions"`
	Workspaces []Workspace `json:"workspaces"`
	Items      int         `json:"items"`
}

var (
	startRe = regexp.MustCompile(`^\[([^\]]+)\] session start: (\S+) \((.*)\)$`)
	endRe   = regexp.MustCompile(`^\[([^\]]+)\] session end: harness=(\S+) duration=(\S+)(?: status=(\S+))?$`)
	featRe  = regexp.MustCompile(`^# (\S+) - (.+)$`)
)

// Discover walks each root for .ai-context directories and builds the KB.
// Nested directories such as .git or node_modules are skipped.
func Discover(roots []string) (*KB, error) {
	k := &KB{ScannedAt: time.Now().Format(time.RFC3339), Roots: roots}
	seen := map[string]bool{}

	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		contextDirs, err := findContextDirs(abs)
		if err != nil {
			return nil, err
		}
		for _, dir := range contextDirs {
			if seen[dir] {
				continue
			}
			seen[dir] = true
			ws, sessions := scanWorkspace(dir)
			k.Workspaces = append(k.Workspaces, ws)
			k.Sessions = append(k.Sessions, sessions...)
		}
	}

	sort.Slice(k.Sessions, func(i, j int) bool {
		return k.Sessions[i].StartedAt > k.Sessions[j].StartedAt
	})
	for i := range k.Sessions {
		k.Sessions[i].Key = fmt.Sprintf("%s#%d", k.Sessions[i].LogFile, i)
	}
	sort.Slice(k.Workspaces, func(i, j int) bool {
		return k.Workspaces[i].Path < k.Workspaces[j].Path
	})
	itemIDs := map[string]bool{}
	for _, s := range k.Sessions {
		itemIDs[s.ContextDir+"|"+s.ID] = true
	}
	k.Items = len(itemIDs)
	return k, nil
}

// findContextDirs returns every .ai-context directory under root (or root
// itself / its child) with a bounded depth.
func findContextDirs(root string) ([]string, error) {
	if base := filepath.Base(root); base == ".ai-context" {
		return []string{root}, nil
	}
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry: skip, keep scanning
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && skipDir(d.Name()) {
			return fs.SkipDir
		}
		depth := strings.Count(strings.TrimPrefix(path, root), string(filepath.Separator))
		if depth > 8 {
			return fs.SkipDir
		}
		if d.Name() == ".ai-context" && path != root {
			found = append(found, path)
			return fs.SkipDir // do not descend into context internals
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("kb: scan %s: %w", root, err)
	}
	// A root that is itself the parent of .ai-context is caught by the walk;
	// also handle root/.ai-context when root exists but walk found nothing.
	return found, nil
}

func skipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build", ".venv",
		"Library", ".cache", "target", "bin", "obj":
		return true
	}
	return strings.HasPrefix(name, ".") && name != ".ai-context"
}

// scanWorkspace parses one .ai-context directory into a Workspace and its
// Sessions.
func scanWorkspace(contextDir string) (Workspace, []Session) {
	ws := Workspace{
		Path:       filepath.Dir(contextDir),
		ContextDir: contextDir,
	}

	// Feature files: id -> parsed fields.
	features := map[string]feature{}
	if entries, err := os.ReadDir(filepath.Join(contextDir, "features")); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := filepath.Join(contextDir, "features", e.Name())
			if f, ok := parseFeature(path); ok {
				features[f.id] = f
				ws.Items++
			}
		}
	}

	// Session logs: one or more start/end pairs per file.
	var all []Session
	if entries, err := os.ReadDir(filepath.Join(contextDir, "sessions")); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
				continue
			}
			path := filepath.Join(contextDir, "sessions", e.Name())
			sessions := parseSessionLog(path, contextDir, features)
			ws.Sessions += len(sessions)
			all = append(all, sessions...)
		}
	}

	// Instruction files: CLAUDE.md / prompt.md inside the workspace and the
	// copilot file one level up.
	for _, rel := range []string{
		filepath.Join(contextDir, "CLAUDE.md"),
		filepath.Join(contextDir, "prompt.md"),
		filepath.Join(filepath.Dir(contextDir), ".github", "copilot-instructions.md"),
	} {
		if content, err := os.ReadFile(rel); err == nil { // #nosec G304 -- fixed candidate paths under the workspace
			ws.Files = append(ws.Files, FileDoc{Path: rel, Content: capContent(string(content))})
		}
	}

	ws.Commits = workspaceCommits(contextDir)
	return ws, all
}

type feature struct {
	id, title, requirement, source, generated, path string
}

// capContent limits file contents sent to the UI (defense against huge files).
func capContent(s string) string {
	const max = 512 * 1024
	if len(s) > max {
		return s[:max] + "\n… (truncated)"
	}
	return s
}

func parseFeature(path string) (feature, bool) {
	data, err := os.ReadFile(path) // #nosec G304 -- path built from ReadDir entries inside .ai-context
	if err != nil {
		return feature{}, false
	}
	f := feature{path: path}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return feature{}, false
	}
	if m := featRe.FindStringSubmatch(strings.TrimRight(lines[0], "\r")); m != nil {
		f.id, f.title = m[1], m[2]
	} else {
		return feature{}, false
	}

	inRequirement := false
	for _, raw := range lines[1:] {
		line := strings.TrimRight(raw, "\r")
		switch {
		case strings.TrimSpace(line) == "## Requirement":
			inRequirement = true
		case strings.HasPrefix(strings.TrimSpace(line), "## "):
			inRequirement = false
		case inRequirement:
			f.requirement += line + "\n"
		case strings.HasPrefix(strings.TrimSpace(line), "- Source:"):
			f.source = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- Source:"))
		case strings.HasPrefix(strings.TrimSpace(line), "- Generated:"):
			f.generated = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- Generated:"))
		}
	}
	f.requirement = strings.Trim(f.requirement, "\n")
	return f, true
}

// parseSessionLog pairs start/end markers within one log file.
func parseSessionLog(path, contextDir string, features map[string]feature) []Session {
	data, err := os.ReadFile(path) // #nosec G304 -- path built from ReadDir entries inside .ai-context
	if err != nil {
		return nil
	}
	rawLines := strings.Split(string(data), "\n")

	base := strings.TrimSuffix(filepath.Base(path), ".log")
	date := ""
	if len(base) >= 10 {
		date = base[:10]
	}

	var sessions []Session
	var cur *Session
	var logLines []string

	flush := func() {
		if cur == nil {
			return
		}
		cur.LogLines = logLines
		sessions = append(sessions, *cur)
		cur = nil
		logLines = nil
	}

	for _, raw := range rawLines {
		line := strings.TrimRight(raw, "\r")
		if line == "" {
			continue
		}
		if m := startRe.FindStringSubmatch(line); m != nil {
			// A start while a session is open closes the previous one as
			// in-progress (interrupted run); stray lines before the first
			// start belong to no session.
			flush()
			logLines = nil
			logLines = append(logLines, line)
			cur = &Session{
				ID:         m[2],
				Title:      m[3],
				Date:       date,
				StartedAt:  m[1],
				LogFile:    path,
				Workspace:  filepath.Dir(contextDir),
				ContextDir: contextDir,
				Status:     "in-progress",
			}
			if f, ok := features[m[2]]; ok {
				cur.Requirement = f.requirement
				cur.SourceURL = f.source
				cur.GeneratedAt = f.generated
				cur.FeatureFile = f.path
				if cur.Title == "" {
					cur.Title = f.title
				}
			}
			continue
		}
		if m := endRe.FindStringSubmatch(line); m != nil && cur != nil {
			logLines = append(logLines, line)
			cur.EndedAt = m[1]
			cur.Harness = m[2]
			cur.Duration = m[3]
			cur.Status = "completed"
			if len(m) > 4 && m[4] == "failed" {
				cur.Status = "failed"
			}
			flush()
			continue
		}
		// Any other line (context notes, commit markers) belongs to the
		// currently open session.
		if cur != nil {
			logLines = append(logLines, line)
		}
	}
	flush()
	return sessions
}

// workspaceCommits returns commits touching .ai-context via go-git. It
// returns nil when the directory is not inside a git repository.
func workspaceCommits(contextDir string) []Commit {
	repo, err := git.PlainOpenWithOptions(contextDir, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil
	}
	root := wt.Filesystem.Root()
	rel, err := filepath.Rel(root, contextDir)
	if err != nil {
		rel = contextDir
	}
	rel = filepath.ToSlash(rel)

	iter, err := repo.Log(&git.LogOptions{
		Order: git.LogOrderCommitterTime,
		PathFilter: func(p string) bool {
			p = filepath.ToSlash(p)
			return p == rel || strings.HasPrefix(p, rel+"/")
		},
	})
	if err != nil {
		return nil
	}
	defer iter.Close()

	var commits []Commit
	for {
		c, err := iter.Next()
		if err != nil {
			break
		}
		msg := strings.TrimSpace(c.Message)
		if idx := strings.IndexByte(msg, '\n'); idx >= 0 {
			msg = msg[:idx]
		}
		commits = append(commits, Commit{
			Hash:    c.Hash.String()[:8],
			Message: msg,
			Author:  c.Author.Name,
			When:    c.Committer.When.Format(time.RFC3339),
		})
		if len(commits) >= 100 {
			break
		}
	}
	return commits
}
