package ticket

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AzureProvider queries Azure DevOps via the WIQL + work item REST APIs.
type AzureProvider struct {
	BaseURL string // e.g. https://dev.azure.com/org/project
	Org     string
	Project string
	PAT     string
	Client  *http.Client
}

// NewAzureProvider builds a provider with a sane HTTP timeout.
func NewAzureProvider(baseURL, org, project, pat string) *AzureProvider {
	return &AzureProvider{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Org:     org,
		Project: project,
		PAT:     pat,
		Client:  &http.Client{Timeout: 15 * time.Second},
	}
}

// GetActiveWorkItems satisfies the Provider interface: WIQL query for the
// assignee/status, then a batched fetch of title and description fields.
func (a *AzureProvider) GetActiveWorkItems(assignee string, status string) ([]WorkItem, error) {
	return a.search(context.Background(), assignee, status)
}

func (a *AzureProvider) search(ctx context.Context, assignee, status string) ([]WorkItem, error) {
	if a.BaseURL == "" {
		return nil, fmt.Errorf("azure: base URL is empty")
	}

	query := fmt.Sprintf(
		"SELECT [System.Id] FROM WorkItems WHERE [System.AssignedTo] = '%s' AND [System.State] = '%s'",
		wiqlEscape(assignee), wiqlEscape(status),
	)
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return nil, fmt.Errorf("azure: encode wiql: %w", err)
	}

	wiqlURL := fmt.Sprintf("%s/%s/_apis/wit/wiql?api-version=7.1", a.BaseURL, a.Project)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, wiqlURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("azure: build wiql request: %w", err)
	}
	a.authorize(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("azure: wiql request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("azure: WIQL HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	var wiql struct {
		WorkItems []struct {
			ID int `json:"id"`
		} `json:"workItems"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wiql); err != nil {
		return nil, fmt.Errorf("azure: decode wiql: %w", err)
	}
	if len(wiql.WorkItems) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(wiql.WorkItems))
	for _, wi := range wiql.WorkItems {
		ids = append(ids, fmt.Sprintf("%d", wi.ID))
	}
	listURL := fmt.Sprintf(
		"%s/%s/_apis/wit/workitems?ids=%s&fields=System.Title,System.Description&api-version=7.1",
		a.BaseURL, a.Project, strings.Join(ids, ","),
	)
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("azure: build list request: %w", err)
	}
	a.authorize(req2)

	resp2, err := a.Client.Do(req2)
	if err != nil {
		return nil, fmt.Errorf("azure: list request failed: %w", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp2.Body, 4096))
		return nil, fmt.Errorf("azure: list HTTP %d: %s", resp2.StatusCode, strings.TrimSpace(string(snippet)))
	}

	var list struct {
		Value []struct {
			ID     int               `json:"id"`
			Fields map[string]string `json:"fields"`
		} `json:"value"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("azure: decode list: %w", err)
	}

	items := make([]WorkItem, 0, len(list.Value))
	for _, wi := range list.Value {
		items = append(items, WorkItem{
			ID:          fmt.Sprintf("%d", wi.ID),
			Title:       wi.Fields["System.Title"],
			Requirement: wi.Fields["System.Description"],
			URL:         fmt.Sprintf("%s/_workitems/edit/%d", a.BaseURL, wi.ID),
		})
	}
	return items, nil
}

// authorize sets Basic auth with an empty user and the PAT as password,
// per Azure DevOps REST conventions.
func (a *AzureProvider) authorize(req *http.Request) {
	raw := ":" + a.PAT
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(raw)))
	req.Header.Set("Accept", "application/json")
}

// wiqlEscape escapes single quotes for WIQL string literals.
func wiqlEscape(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
