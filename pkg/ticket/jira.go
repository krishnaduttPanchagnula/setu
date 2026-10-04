package ticket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// JiraProvider queries the Jira REST API v2 for assigned, ready work items.
type JiraProvider struct {
	BaseURL  string
	Username string
	Token    string
	Client   *http.Client
}

// NewJiraProvider builds a provider with a sane HTTP timeout.
func NewJiraProvider(baseURL, username, token string) *JiraProvider {
	return &JiraProvider{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		Username: username,
		Token:    token,
		Client:   &http.Client{Timeout: 15 * time.Second},
	}
}

// GetActiveWorkItems runs a JQL search for assignee + status (Provider impl).
func (j *JiraProvider) GetActiveWorkItems(assignee string, status string) ([]WorkItem, error) {
	return j.search(context.Background(), assignee, status)
}

func (j *JiraProvider) search(ctx context.Context, assignee, status string) ([]WorkItem, error) {
	if j.BaseURL == "" {
		return nil, fmt.Errorf("jira: base URL is empty")
	}
	jql := fmt.Sprintf(`assignee = "%s" AND status = "%s"`, assignee, status)
	q := url.Values{}
	q.Set("jql", jql)
	q.Set("fields", "summary,description")
	q.Set("maxResults", "100")

	endpoint := fmt.Sprintf("%s/rest/api/2/search?%s", j.BaseURL, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("jira: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if j.Token != "" {
		req.Header.Set("Authorization", "Bearer "+j.Token)
	}

	resp, err := j.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jira: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("jira: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Issues []struct {
			Key    string `json:"key"`
			Fields struct {
				Summary     string `json:"summary"`
				Description string `json:"description"`
			} `json:"fields"`
		} `json:"issues"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("jira: decode response: %w", err)
	}

	items := make([]WorkItem, 0, len(payload.Issues))
	for _, issue := range payload.Issues {
		items = append(items, WorkItem{
			ID:          issue.Key,
			Title:       issue.Fields.Summary,
			Requirement: issue.Fields.Description,
			URL:         j.BaseURL + "/browse/" + issue.Key,
		})
	}
	return items, nil
}
