// Package ticket implements the Ticketing Engine (PRD section 2.1/2.3).
// Providers fetch issues from Jira, Azure DevOps or manual input and return
// the unified WorkItem struct consumed by the context builder.
package ticket

import "fmt"

// WorkItem is the provider-agnostic representation of a unit of work.
type WorkItem struct {
	ID          string // e.g. PROJ-123
	Title       string
	Requirement string
	URL         string
}

// Provider decouples the requirement source from session initialization.
type Provider interface {
	GetActiveWorkItems(assignee string, status string) ([]WorkItem, error)
}

// ValidationError reports a structurally invalid WorkItem.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("ticket: invalid %s: %s", e.Field, e.Message)
}

// Validate enforces the minimum contract for a selectable work item.
func (w *WorkItem) Validate() error {
	if w.ID == "" {
		return &ValidationError{Field: "ID", Message: "required"}
	}
	if w.Title == "" {
		return &ValidationError{Field: "Title", Message: "required"}
	}
	return nil
}

// String renders a one-line entry for interactive menus.
func (w WorkItem) String() string {
	return fmt.Sprintf("%s - %s", w.ID, w.Title)
}

// Compile-time assertions: every provider must satisfy the interface (PRD 2.3).
var (
	_ Provider = (*JiraProvider)(nil)
	_ Provider = (*AzureProvider)(nil)
	_ Provider = (*ManualProvider)(nil)
)
