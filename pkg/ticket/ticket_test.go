package ticket

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestWorkItemValidate(t *testing.T) {
	valid := WorkItem{ID: "PROJ-1", Title: "Title"}
	if err := valid.Validate(); err != nil {
		t.Errorf("valid item rejected: %v", err)
	}

	noID := WorkItem{Title: "T"}
	if err := noID.Validate(); err == nil {
		t.Error("missing ID accepted")
	} else if !strings.Contains(err.Error(), "ID") {
		t.Errorf("error should name field: %v", err)
	}

	noTitle := WorkItem{ID: "PROJ-2"}
	if err := noTitle.Validate(); err == nil {
		t.Error("missing Title accepted")
	}
}

func TestWorkItemString(t *testing.T) {
	w := WorkItem{ID: "PROJ-123", Title: "Fix login"}
	if got, want := w.String(), "PROJ-123 - Fix login"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// TestJiraGetActiveWorkItems exercises the Jira REST flow against a fake server.
func TestJiraGetActiveWorkItems(t *testing.T) {
	var gotJQL, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/2/search" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		gotJQL = r.URL.Query().Get("jql")
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issues": []map[string]any{
				{
					"key": "PROJ-42",
					"fields": map[string]any{
						"summary":     "Implement token refresh",
						"description": "Refresh tokens must rotate silently.",
					},
				},
				{
					"key": "PROJ-43",
					"fields": map[string]any{
						"summary":     "Harden login form",
						"description": "Add rate limiting.",
					},
				},
			},
		})
	}))
	defer server.Close()

	p := NewJiraProvider(server.URL+"/", "dev@company.com", "secret-token")
	items, err := p.GetActiveWorkItems("dev@company.com", "ready-to-work")
	if err != nil {
		t.Fatalf("GetActiveWorkItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].ID != "PROJ-42" || items[0].Title != "Implement token refresh" {
		t.Errorf("item[0] = %+v", items[0])
	}
	if items[0].URL != server.URL+"/browse/PROJ-42" {
		t.Errorf("URL = %q", items[0].URL)
	}
	if !strings.Contains(gotJQL, "ready-to-work") || !strings.Contains(gotJQL, "dev@company.com") {
		t.Errorf("JQL = %q", gotJQL)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("auth header = %q", gotAuth)
	}
}

// TestJiraServerError ensures HTTP errors surface with status and body.
func TestJiraServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	p := NewJiraProvider(server.URL, "", "bad")
	if _, err := p.GetActiveWorkItems("a", "s"); err == nil {
		t.Fatal("expected error on 401")
	} else if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should include status: %v", err)
	}
}

// TestJiraEmptyBaseURL guards against malformed request URLs.
func TestJiraEmptyBaseURL(t *testing.T) {
	p := NewJiraProvider("", "", "")
	if _, err := p.GetActiveWorkItems("a", "s"); err == nil {
		t.Fatal("expected error for empty base URL")
	}
}

// TestAzureGetActiveWorkItems exercises the WIQL + batch fetch flow.
func TestAzureGetActiveWorkItems(t *testing.T) {
	var wiqlBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_apis/wit/wiql"):
			if r.Method != http.MethodPost {
				t.Errorf("wiql method = %s", r.Method)
			}
			_ = json.NewDecoder(r.Body).Decode(&wiqlBody)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"workItems": []map[string]int{{"id": 7}, {"id": 9}},
			})
		case strings.HasSuffix(r.URL.Path, "/_apis/wit/workitems"):
			if !strings.Contains(r.URL.RawQuery, "ids=7,9") {
				t.Errorf("ids query = %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"value": []map[string]any{
					{"id": 7, "fields": map[string]string{
						"System.Title":       "Azure item one",
						"System.Description": "Do the thing.",
					}},
					{"id": 9, "fields": map[string]string{
						"System.Title":       "Azure item two",
						"System.Description": "Do the other thing.",
					}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	p := NewAzureProvider(server.URL, "org", "proj", "pat-123")
	items, err := p.GetActiveWorkItems("dev@company.com", "ready-to-work")
	if err != nil {
		t.Fatalf("GetActiveWorkItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].ID != "7" || items[0].Title != "Azure item one" {
		t.Errorf("item[0] = %+v", items[0])
	}
	if !strings.Contains(wiqlBody["query"], "ready-to-work") {
		t.Errorf("wiql query = %q", wiqlBody["query"])
	}
}

// TestManualProviderFromFile loads work items from a JSON file.
func TestManualProviderFromFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/items.json"
	raw := `[{"ID":"M-1","Title":"Manual item","Requirement":"Do it","URL":"https://x/1"}]`
	if err := write(path, raw); err != nil {
		t.Fatal(err)
	}

	p, err := NewManualProvider(path)
	if err != nil {
		t.Fatalf("NewManualProvider: %v", err)
	}
	items, err := p.GetActiveWorkItems("", "ready-to-work")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "M-1" {
		t.Errorf("items = %+v", items)
	}
}

func TestManualProviderFromFlag(t *testing.T) {
	p := NewFromFlag("PROJ-9", "  Title  ", "req")
	items, _ := p.GetActiveWorkItems("", "")
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	if items[0].ID != "PROJ-9" || items[0].Title != "Title" {
		t.Errorf("item = %+v", items[0])
	}
	if err := items[0].Validate(); err != nil {
		t.Errorf("flag item invalid: %v", err)
	}
}

func write(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
