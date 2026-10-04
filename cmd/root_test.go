package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"

	"setu/pkg/ticket"
)

// writeTestConfig writes a config.yaml with a test harness (echo) so no real
// AI tool is required during tests.
func writeTestConfig(t *testing.T, extra string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
core:
  default_harness: "claude-code"
  workspace_dir: ".ai-context"
ticketing:
  provider: "manual"
  status_filter: "ready-to-work"
harnesses:
  claude-code:
    command: "echo"
    args: ["delegated-session-started"]
  fake-harness:
    command: "echo"
    args: ["delegated-session-started"]
` + extra
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestExecuteUnknownCommand ensures bad input returns exit code 2 and usage.
func TestExecuteUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Execute([]string{"frobnicate"}, strings.NewReader(""), &out, &errOut)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "unknown command") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestExecuteNoArgsPrintsUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Execute(nil, strings.NewReader(""), &out, &errOut)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "setu start") {
		t.Error("usage text missing")
	}
}

func TestExecuteVersionAndHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Execute([]string{"version"}, nil, &out, &errOut); code != 0 {
		t.Errorf("version exit = %d", code)
	}
	if !strings.Contains(out.String(), Version) {
		t.Errorf("version output = %q", out.String())
	}
	out.Reset()
	if code := Execute([]string{"help"}, nil, &out, &errOut); code != 0 {
		t.Errorf("help exit = %d", code)
	}
	if !strings.Contains(out.String(), "Usage:") || !strings.Contains(out.String(), "setu start") {
		t.Errorf("help output missing usage text: %q", out.String())
	}
}

// TestStartDryRunFullFlow runs the PRD 1.2 flow end to end without a real
// harness: manual ticket -> selection -> context build -> persistence.
func TestStartDryRunFullFlow(t *testing.T) {
	t.Chdir(t.TempDir())
	cfgPath := writeTestConfig(t, "")

	stdin := strings.NewReader("1\n")
	var out, errOut bytes.Buffer
	code := Execute([]string{
		"start",
		"--config", cfgPath,
		"--manual",
		"--ticket", "PROJ-55",
		"--title", "Wire up session menu",
		"--requirement", "The menu must list ready tickets.",
		"--dry-run",
	}, stdin, &out, &errOut)

	if code != 0 {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	}

	// PRD 2.5 layout assertions.
	for _, f := range []string{
		".ai-context/features/PROJ-55.md",
		".ai-context/CLAUDE.md",
	} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("missing artifact %s: %v", f, err)
		}
	}
	body, err := os.ReadFile(".ai-context/features/PROJ-55.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "The menu must list ready tickets.") {
		t.Error("requirement not rendered")
	}
	if !strings.Contains(out.String(), "Dry run") {
		t.Errorf("stdout should mention dry run: %s", out.String())
	}
	// Default flow tries a git commit but the temp dir is not a repo: it
	// must report the failure without crashing (exit stays 0).
	if !strings.Contains(errOut.String(), "git commit") {
		t.Errorf("expected non-fatal git error note, stderr: %s", errOut.String())
	}
}

// TestStartCommitsToGit verifies auto-commit persistence (PRD 1.2 step 6).
func TestStartCommitsToGit(t *testing.T) {
	dir := t.TempDir()
	if _, err := git.PlainInit(dir, false); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	cfgPath := writeTestConfig(t, "")

	stdin := strings.NewReader("1\n")
	var out, errOut bytes.Buffer
	code := Execute([]string{
		"start",
		"--config", cfgPath,
		"--manual",
		"--ticket", "PROJ-77",
		"--title", "Commit persistence",
		"--requirement", "Session artifacts must be committed.",
		"--dry-run",
	}, stdin, &out, &errOut)

	if code != 0 {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "Committed workspace") {
		t.Errorf("expected commit confirmation, stdout: %s", out.String())
	}

	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatalf("no commit created: %v", err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(commit.Message, "PROJ-77") {
		t.Errorf("commit message = %q", commit.Message)
	}
	tree, err := commit.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.File(".ai-context/features/PROJ-77.md"); err != nil {
		t.Errorf("feature file not committed: %v", err)
	}
}

// TestStartExecutesHarness runs the delegation step against a fake command
// and asserts its output reached our stdout (PRD 2.4 stdio binding).
func TestStartExecutesHarness(t *testing.T) {
	t.Chdir(t.TempDir())
	cfgPath := writeTestConfig(t, "")

	var out, errOut bytes.Buffer
	code := Execute([]string{
		"start",
		"--config", cfgPath,
		"--manual",
		"--ticket", "PROJ-88",
		"--title", "Delegation",
		"--requirement", "R",
		"--no-commit",
	}, strings.NewReader("1\n"), &out, &errOut)

	if code != 0 {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "delegated-session-started") {
		t.Errorf("harness output missing from stdout: %s", out.String())
	}
	if !strings.Contains(out.String(), "Session complete.") {
		t.Errorf("missing completion marker: %s", out.String())
	}
	// Session log must record the run.
	logs, _ := filepath.Glob(filepath.Join(".ai-context", "sessions", "*.log"))
	if len(logs) != 1 {
		t.Fatalf("session logs = %v", logs)
	}
	logBody, _ := os.ReadFile(logs[0])
	if !strings.Contains(string(logBody), "session end") {
		t.Error("session end note missing from log")
	}
}

// TestStartConfluencePush verifies the optional Confluence persistence leg.
func TestStartConfluencePush(t *testing.T) {
	t.Chdir(t.TempDir())

	var gotAuth string
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"777"}`))
	}))
	defer server.Close()

	cfgPath := writeTestConfig(t, `
confluence:
  enabled: true
  base_url: "`+server.URL+`"
  space_key: "ENG"
  parent_page_id: "42"
`)
	t.Setenv("AI_HARNESS_CONFLUENCE_TOKEN", "conf-secret")

	var out, errOut bytes.Buffer
	code := Execute([]string{
		"start",
		"--config", cfgPath,
		"--manual",
		"--ticket", "PROJ-90",
		"--title", "Confluence push",
		"--requirement", "Summaries must reach the knowledge base.",
		"--dry-run",
		"--no-commit",
	}, strings.NewReader("1\n"), &out, &errOut)

	if code != 0 {
		t.Fatalf("exit code = %d\nstderr: %s", code, errOut.String())
	}
	if gotAuth != "Bearer conf-secret" {
		t.Errorf("auth header = %q (env binding failed?)", gotAuth)
	}
	if payload["type"] != "page" {
		t.Errorf("payload type = %v", payload["type"])
	}
	if !strings.Contains(out.String(), "Pushed Confluence page 777") {
		t.Errorf("missing push confirmation: %s", out.String())
	}
}

// TestStartNoWorkItems is the empty-result path.
func TestStartNoWorkItems(t *testing.T) {
	t.Chdir(t.TempDir())
	cfgPath := writeTestConfig(t, "")

	var out, errOut bytes.Buffer
	code := Execute([]string{
		"start", "--config", cfgPath, "--manual", "--dry-run",
	}, strings.NewReader(""), &out, &errOut)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "no work items") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

// TestStartSelectsFromMenu covers the interactive multi-item menu (PRD 1.2
// step 3), including out-of-range and cancel handling.
func TestStartSelectsFromMenu(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := t.TempDir()
	itemsPath := filepath.Join(dir, "work-items.json")
	items := []map[string]string{
		{"ID": "A-1", "Title": "First", "Requirement": "R1"},
		{"ID": "A-2", "Title": "Second", "Requirement": "R2"},
	}
	raw, _ := json.Marshal(items)
	if err := os.WriteFile(itemsPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeTestConfig(t, "")
	// Manual provider file sits next to config as {config}-work-items.json.
	if err := os.Rename(itemsPath, strings.TrimSuffix(cfgPath, ".yaml")+"-work-items.json"); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Execute([]string{
		"start", "--config", cfgPath, "--manual", "--dry-run", "--no-commit",
	}, strings.NewReader("2\n"), &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "[2] A-2 - Second") {
		t.Errorf("menu did not render items: %s", out.String())
	}
	if !strings.Contains(out.String(), "Selected: A-2 - Second") {
		t.Errorf("selection not applied: %s", out.String())
	}
	if _, err := os.Stat(".ai-context/features/A-2.md"); err != nil {
		t.Errorf("wrong item selected: %v", err)
	}

	// Invalid choice path.
	out.Reset()
	errOut.Reset()
	code = Execute([]string{
		"start", "--config", cfgPath, "--manual", "--dry-run", "--no-commit",
	}, strings.NewReader("99\n"), &out, &errOut)
	if code != 1 {
		t.Errorf("out-of-range exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "out of range") {
		t.Errorf("stderr = %q", errOut.String())
	}

	// Cancel path.
	code = Execute([]string{
		"start", "--config", cfgPath, "--manual", "--dry-run", "--no-commit",
	}, strings.NewReader("q\n"), &out, &errOut)
	if code != 1 {
		t.Errorf("cancel exit = %d, want 1", code)
	}
}

// TestStartManualFlagProvider covers the single flagged work item path
// (--manual + --ticket + --title + --requirement).
func TestStartManualFlagProvider(t *testing.T) {
	t.Chdir(t.TempDir())
	cfgPath := writeTestConfig(t, "")

	var out, errOut bytes.Buffer
	code := Execute([]string{
		"start", "--config", cfgPath, "--manual",
		"--ticket", "MISSING-1", "--title", "x", "--requirement", "y",
		"--dry-run",
	}, strings.NewReader(""), &out, &errOut)

	if code != 0 {
		t.Fatalf("exit code = %d\nstderr: %s", code, errOut.String())
	}
	if _, err := os.Stat(".ai-context/features/MISSING-1.md"); err != nil {
		t.Errorf("flagged item not built: %v", err)
	}
}

// TestListCommand prints items without starting a session.
func TestListCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	cfgPath := writeTestConfig(t, "")
	dir := filepath.Dir(cfgPath)
	items := []map[string]string{{"ID": "L-1", "Title": "Listed", "Requirement": "R", "URL": "https://x/1"}}
	raw, _ := json.Marshal(items)
	if err := os.WriteFile(filepath.Join(dir, "config-work-items.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Execute([]string{"list", "--config", cfgPath, "--manual"}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "L-1\tListed\thttps://x/1") {
		t.Errorf("list output = %q", out.String())
	}
}

// TestConfigInit writes the example config (PRD 2.2) exactly once.
func TestConfigInit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	var out, errOut bytes.Buffer

	code := Execute([]string{"config", "init", "--config", path}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config not written: %v", err)
	}

	code = Execute([]string{"config", "init", "--config", path}, nil, &out, &errOut)
	if code != 1 {
		t.Errorf("second init exit = %d, want 1 (no clobber)", code)
	}
}

// TestParseFlagsRejectsUnknown covers flag validation.
func TestParseFlagsRejectsUnknown(t *testing.T) {
	if _, err := parseFlags([]string{"--bogus"}); err == nil {
		t.Fatal("expected error for unknown flag")
	}
	if _, err := parseFlags([]string{"--config"}); err == nil {
		t.Fatal("expected error for missing flag value")
	}
}

// TestStartHarnessFailureStillPersistsAndReportsFailure pins the failure
// contract: a crashing harness still persists context (session log + git
// commit) but the process exit code is non-zero so callers are not misled.
func TestStartHarnessFailureStillPersistsAndReportsFailure(t *testing.T) {
	dir := t.TempDir()
	if _, err := git.PlainInit(dir, false); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	cfgPath := filepath.Join(dir, "config.yaml")
	yaml := `
core:
  default_harness: "claude-code"
  workspace_dir: ".ai-context"
ticketing:
  provider: "manual"
harnesses:
  claude-code:
    command: "false"
    args: []
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Execute([]string{
		"start", "--config", cfgPath, "--manual",
		"--ticket", "PROJ-FAIL", "--title", "Crash", "--requirement", "R",
	}, strings.NewReader("1\n"), &out, &errOut)

	// Failure must be reported via exit code.
	if code != 1 {
		t.Errorf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "harness error") {
		t.Errorf("stderr missing harness error: %s", errOut.String())
	}
	// Persistence must still have happened.
	if !strings.Contains(out.String(), "Committed workspace") {
		t.Errorf("context was not committed after failure: %s", out.String())
	}
	logs, _ := filepath.Glob(filepath.Join(".ai-context", "sessions", "*.log"))
	if len(logs) != 1 {
		t.Fatalf("session logs = %v", logs)
	}
	logBody, _ := os.ReadFile(logs[0])
	if !strings.Contains(string(logBody), "session end") {
		t.Error("session end note missing after harness failure")
	}
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Head(); err != nil {
		t.Errorf("no commit created after failure: %v", err)
	}
	if !strings.Contains(out.String(), "harness failure") {
		t.Errorf("stdout should mention harness failure: %s", out.String())
	}
}

// fakeJiraServer returns a Jira search endpoint with n fixed issues.
func fakeJiraServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issues": []map[string]any{
				{"key": "PROJ-42", "fields": map[string]any{
					"summary": "First issue", "description": "R1",
				}},
				{"key": "PROJ-43", "fields": map[string]any{
					"summary": "Second issue", "description": "R2",
				}},
			},
		})
	}))
}

// jiraTestConfig writes a config that points ticketing at the fake server.
func jiraTestConfig(t *testing.T, url string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := `
core:
  default_harness: "claude-code"
  workspace_dir: ".ai-context"
ticketing:
  provider: "jira"
  status_filter: "ready-to-work"
  assignee: "dev@company.com"
  jira:
    url: "` + url + `"
    username: "dev@company.com"
harnesses:
  claude-code:
    command: "echo"
    args: ["sim"]
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestStartTicketNarrowingWithJiraProvider verifies --ticket filters the
// provider result down to exactly one work item (PRD 1.2 step 2+3), and
// exercises resolveProvider's jira branch.
func TestStartTicketNarrowingWithJiraProvider(t *testing.T) {
	t.Chdir(t.TempDir())
	server := fakeJiraServer(t)
	defer server.Close()
	cfgPath := jiraTestConfig(t, server.URL)

	var out, errOut bytes.Buffer
	code := Execute([]string{
		"start", "--config", cfgPath,
		"--ticket", "PROJ-43", "--dry-run", "--no-commit",
	}, strings.NewReader("1\n"), &out, &errOut)

	if code != 0 {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(".ai-context/features/PROJ-43.md"); err != nil {
		t.Errorf("selected ticket not built: %v", err)
	}
	if _, err := os.Stat(".ai-context/features/PROJ-42.md"); err == nil {
		t.Error("non-selected ticket was built")
	}
}

// TestStartProviderFetchErrorPropagates: a failing tracker API must fail
// the start command with a clear error (exit 1), not a crash.
func TestStartProviderFetchErrorPropagates(t *testing.T) {
	t.Chdir(t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()
	cfgPath := jiraTestConfig(t, server.URL)

	var out, errOut bytes.Buffer
	code := Execute([]string{"start", "--config", cfgPath}, strings.NewReader("1\n"), &out, &errOut)

	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "fetch work items") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

// TestResolveProviderUnsupported covers the unsupported-provider branch.
func TestResolveProviderUnsupported(t *testing.T) {
	t.Chdir(t.TempDir())
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	yaml := `
core:
  default_harness: "claude-code"
ticketing:
  provider: "bogus"
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Execute([]string{"start", "--config", cfgPath}, strings.NewReader(""), &out, &errOut)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "unsupported ticketing provider") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

// TestStartMissingJiraURL: provider jira without a URL must fail cleanly.
func TestStartMissingJiraURL(t *testing.T) {
	t.Chdir(t.TempDir())
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	yaml := `
core:
  default_harness: "claude-code"
ticketing:
  provider: "jira"
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Execute([]string{"start", "--config", cfgPath}, strings.NewReader(""), &out, &errOut)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "jira.url is not set") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

// TestStartBadConfigPathExitCode: an explicit missing config is fatal.
func TestStartBadConfigPathExitCode(t *testing.T) {
	t.Chdir(t.TempDir())
	var out, errOut bytes.Buffer
	code := Execute([]string{"start", "--config", "/nonexistent/config.yaml"}, nil, &out, &errOut)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
}

// TestStartUnknownHarnessFallsBackToName: unknown harness names fall back
// to name-as-command (config.HarnessSpec); the exec failure is reported as
// a harness error with a non-zero exit.
func TestStartUnknownHarnessFallsBackToName(t *testing.T) {
	t.Chdir(t.TempDir())
	cfgPath := writeTestConfig(t, "")

	var out, errOut bytes.Buffer
	code := Execute([]string{
		"start", "--config", cfgPath, "--harness", "ghost-harness-xyz",
		"--manual", "--ticket", "PROJ-G", "--title", "T", "--requirement", "R",
		"--dry-run", "--no-commit",
	}, strings.NewReader("1\n"), &out, &errOut)

	// dry-run skips execution, so this must succeed and note the harness.
	if code != 0 {
		t.Fatalf("dry-run exit = %d\nstderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Prepared ghost-harness-xyz") {
		t.Errorf("stdout = %q", out.String())
	}

	// Now without dry-run: exec fails (binary missing) -> exit 1.
	out.Reset()
	errOut.Reset()
	code = Execute([]string{
		"start", "--config", cfgPath, "--harness", "ghost-harness-xyz",
		"--manual", "--ticket", "PROJ-G", "--title", "T", "--requirement", "R",
		"--no-commit",
	}, strings.NewReader("1\n"), &out, &errOut)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "ghost-harness-xyz") {
		t.Errorf("stderr should name the harness: %q", errOut.String())
	}
	// Context must still persist despite the failure.
	if _, err := os.Stat(".ai-context/features/PROJ-G.md"); err != nil {
		t.Errorf("context missing after harness failure: %v", err)
	}
}

// TestStartConfluencePushFailureIsNonFatal: a Confluence outage must not
// lose the local session (exit stays 0), but is surfaced on stderr.
func TestStartConfluencePushFailureIsNonFatal(t *testing.T) {
	t.Chdir(t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusInternalServerError)
	}))
	defer server.Close()

	cfgPath := writeTestConfig(t, `
confluence:
  enabled: true
  base_url: "`+server.URL+`"
  space_key: "ENG"
`)
	t.Setenv("AI_HARNESS_CONFLUENCE_TOKEN", "tok")

	var out, errOut bytes.Buffer
	code := Execute([]string{
		"start", "--config", cfgPath, "--manual",
		"--ticket", "PROJ-C", "--title", "T", "--requirement", "R",
		"--dry-run", "--no-commit",
	}, strings.NewReader("1\n"), &out, &errOut)

	if code != 0 {
		t.Errorf("exit = %d, want 0 (confluence failure is non-fatal)", code)
	}
	if !strings.Contains(errOut.String(), "confluence push") {
		t.Errorf("stderr missing confluence error: %q", errOut.String())
	}
	if !strings.Contains(out.String(), "Session complete.") {
		t.Errorf("stdout = %q", out.String())
	}
}

// TestSelectItemInvalidInput covers non-numeric and EOF menu responses.
func TestSelectItemInvalidInput(t *testing.T) {
	items := []ticket.WorkItem{
		{ID: "A-1", Title: "First"},
		{ID: "A-2", Title: "Second"},
	}

	var out bytes.Buffer
	if _, err := selectItem(items, strings.NewReader("abc\n"), &out); err == nil {
		t.Error("non-numeric choice accepted")
	} else if !strings.Contains(err.Error(), "invalid choice") {
		t.Errorf("err = %v", err)
	}

	out.Reset()
	if _, err := selectItem(items, strings.NewReader(""), &out); err == nil {
		t.Error("EOF accepted as a selection")
	} else if !strings.Contains(err.Error(), "no selection provided") {
		t.Errorf("err = %v", err)
	}
}

// TestSelectItemEmptyList and single-item auto-select behavior.
func TestSelectItemEdgeCases(t *testing.T) {
	var out bytes.Buffer
	if _, err := selectItem(nil, strings.NewReader(""), &out); err == nil {
		t.Error("empty list should error")
	}

	out.Reset()
	item, err := selectItem([]ticket.WorkItem{{ID: "S-1", Title: "Solo"}}, strings.NewReader(""), &out)
	if err != nil {
		t.Fatalf("single item should auto-select: %v", err)
	}
	if item.ID != "S-1" {
		t.Errorf("item = %q", item.ID)
	}
	if !strings.Contains(out.String(), "Selected: S-1 - Solo") {
		t.Errorf("stdout = %q", out.String())
	}
}

// fakeAzureServer returns WIQL + batch work item endpoints with two issues.
func fakeAzureServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_apis/wit/wiql"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"workItems": []map[string]int{{"id": 11}, {"id": 12}},
			})
		case strings.HasSuffix(r.URL.Path, "/_apis/wit/workitems"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"value": []map[string]any{
					{"id": 11, "fields": map[string]string{
						"System.Title": "Azure one", "System.Description": "R1",
					}},
					{"id": 12, "fields": map[string]string{
						"System.Title": "Azure two", "System.Description": "R2",
					}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

// TestStartAzureProviderBranch exercises resolveProvider's azure wiring
// end to end: WIQL query, field fetch, context build, exit 0.
func TestStartAzureProviderBranch(t *testing.T) {
	t.Chdir(t.TempDir())
	server := fakeAzureServer(t)
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	yaml := `
core:
  default_harness: "claude-code"
  workspace_dir: ".ai-context"
ticketing:
  provider: "azure"
  status_filter: "ready-to-work"
  assignee: "dev@company.com"
  azure:
    url: "` + server.URL + `"
    org: "org"
    project: "proj"
harnesses:
  claude-code:
    command: "echo"
    args: ["sim"]
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Execute([]string{
		"start", "--config", cfgPath, "--ticket", "12",
		"--dry-run", "--no-commit",
	}, strings.NewReader("1\n"), &out, &errOut)

	if code != 0 {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(".ai-context/features/12.md"); err != nil {
		t.Errorf("azure work item 12 not built: %v", err)
	}
	if _, err := os.Stat(".ai-context/features/11.md"); err == nil {
		t.Error("non-selected azure work item 11 was built")
	}
}

// TestStartMissingAzureURL: provider azure without a URL must fail cleanly.
func TestStartMissingAzureURL(t *testing.T) {
	t.Chdir(t.TempDir())
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	yaml := `
core:
  default_harness: "claude-code"
ticketing:
  provider: "azure"
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Execute([]string{"start", "--config", cfgPath}, strings.NewReader(""), &out, &errOut)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "azure.url is not set") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

// TestListEmptyOutput: manual mode with no items prints a friendly notice.
func TestListEmptyOutput(t *testing.T) {
	t.Chdir(t.TempDir())
	cfgPath := writeTestConfig(t, "")

	var out, errOut bytes.Buffer
	code := Execute([]string{"list", "--config", cfgPath, "--manual"}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "(no work items)") {
		t.Errorf("stdout = %q", out.String())
	}
}
