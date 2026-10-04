package ui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"setu/pkg/kb"
)

func fixtureKB(t *testing.T) (*kb.KB, Provider) {
	t.Helper()
	root := t.TempDir()
	ctx := filepath.Join(root, ".ai-context")
	if err := os.MkdirAll(filepath.Join(ctx, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ctx, "features"), 0o755); err != nil {
		t.Fatal(err)
	}
	log := "[2026-10-04T10:00:00Z] session start: PROJ-1 (Add login page)\n" +
		"[2026-10-04T10:05:00Z] session end: harness=claude-code duration=5m0s status=ok\n"
	if err := os.WriteFile(filepath.Join(ctx, "sessions", "2026-10-04_PROJ-1.log"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	feature := "# PROJ-1 - Add login page\n\n## Requirement\n\nLogin <b>must</b> work.\n"
	if err := os.WriteFile(filepath.Join(ctx, "features", "PROJ-1.md"), []byte(feature), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ctx, "CLAUDE.md"), []byte("# Active Work Item: PROJ-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	provider := DiscoverRoots([]string{root})
	k, err := provider()
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	return k, provider
}

func TestServeIndexPage(t *testing.T) {
	_, provider := fixtureKB(t)
	srv := httptest.NewServer(NewHandler(provider))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
	body := readAll(t, resp)
	for _, want := range []string{"setu", "knowledge base", "/api/kb"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestServeKBJson(t *testing.T) {
	_, provider := fixtureKB(t)
	srv := httptest.NewServer(NewHandler(provider))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/kb")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var payload struct {
		Sessions []struct {
			ID              string `json:"id"`
			Status          string `json:"status"`
			RequirementHTML string `json:"requirementHtml"`
		} `json:"sessions"`
		Workspaces []struct {
			Files []kb.FileDoc `json:"files"`
		} `json:"workspaces"`
		Items int `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(payload.Sessions))
	}
	s := payload.Sessions[0]
	if s.ID != "PROJ-1" || s.Status != "completed" {
		t.Errorf("session = %+v", s)
	}
	// Raw HTML in the requirement must be escaped in the rendered output.
	if strings.Contains(s.RequirementHTML, "<b>must</b>") {
		t.Errorf("requirementHtml not escaped: %s", s.RequirementHTML)
	}
	if !strings.Contains(s.RequirementHTML, "&lt;b&gt;") {
		t.Errorf("requirementHtml missing escaped markup: %s", s.RequirementHTML)
	}
	if len(payload.Workspaces) != 1 || len(payload.Workspaces[0].Files) != 1 {
		t.Errorf("workspaces/files = %+v", payload.Workspaces)
	}
	if payload.Items != 1 {
		t.Errorf("items = %d", payload.Items)
	}
}

func TestServeUnknownPathIs404(t *testing.T) {
	_, provider := fixtureKB(t)
	srv := httptest.NewServer(NewHandler(provider))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestServeProviderErrorIs500(t *testing.T) {
	handler := NewHandler(func() (*kb.KB, error) { return nil, errors.New("boom") })
	req := httptest.NewRequest(http.MethodGet, "/api/kb", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestSummary(t *testing.T) {
	if got := Summary(nil); !strings.Contains(got, "no runs") {
		t.Errorf("Summary(nil) = %q", got)
	}
	_, provider := fixtureKB(t)
	k, _ := provider()
	if got := Summary(k); !strings.Contains(got, "1 run(s)") {
		t.Errorf("Summary = %q", got)
	}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String()
}
