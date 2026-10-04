package ticket

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ManualProvider reads work items from a local JSON file (or an inline list)
// for offline development and tests. It satisfies the Provider interface.
type ManualProvider struct {
	Items []WorkItem
}

// NewManualProvider loads items from path; an empty path yields an empty list.
func NewManualProvider(path string) (*ManualProvider, error) {
	if path == "" {
		return &ManualProvider{}, nil
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- path is operator-supplied (local config/flag), never remote input
	if err != nil {
		return nil, fmt.Errorf("manual: read %s: %w", path, err)
	}
	var items []WorkItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("manual: parse %s: %w", path, err)
	}
	return &ManualProvider{Items: items}, nil
}

// GetActiveWorkItems returns all configured manual items. Manual mode is
// offline by design: assignee and status filters do not apply.
func (m *ManualProvider) GetActiveWorkItems(assignee string, status string) ([]WorkItem, error) {
	out := make([]WorkItem, 0, len(m.Items))
	out = append(out, m.Items...)
	return out, nil
}

// NewFromFlag builds a single-item provider from --ticket/--title flags.
func NewFromFlag(id, title, requirement string) *ManualProvider {
	return &ManualProvider{Items: []WorkItem{{
		ID:          strings.TrimSpace(id),
		Title:       strings.TrimSpace(title),
		Requirement: requirement,
	}}}
}
