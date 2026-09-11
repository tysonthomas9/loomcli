package dto

// CreateIssueRequest represents the JSON request body for creating an issue.
// Field set matches the current IssueCreateRequest in handlers_issues.go.
type CreateIssueRequest struct {
	// Required fields
	Title     string `json:"title"`
	IssueType string `json:"issue_type"`
	Priority  int    `json:"priority"` // No omitempty: 0 is valid (P0/critical)

	// Optional fields
	ID                   string   `json:"id,omitempty"`
	Parent               string   `json:"parent,omitempty"`
	Description          string   `json:"description,omitempty"`
	Status               string   `json:"status,omitempty"`
	Design               string   `json:"design,omitempty"`
	AcceptanceCriteria   string   `json:"acceptance_criteria,omitempty"`
	Notes                string   `json:"notes,omitempty"`
	Assignee             string   `json:"assignee,omitempty"`
	Owner                string   `json:"owner,omitempty"`
	CreatedBy            string   `json:"created_by,omitempty"`
	ExternalRef          string   `json:"external_ref,omitempty"`
	EstimatedMinutes     *int     `json:"estimated_minutes,omitempty"` // Pointer: 0 is valid, distinct from unset
	Labels               []string `json:"labels,omitempty"`
	Dependencies         []string `json:"dependencies,omitempty"`
	DueAt                string   `json:"due_at,omitempty"`
	DeferUntil           string   `json:"defer_until,omitempty"`
	SourceRepo           string   `json:"source_repo,omitempty"`
	PrimaryRepository    string   `json:"primary_repository,omitempty"`
	SelectedRepositories []string `json:"selected_repositories,omitempty"`
}

// PatchIssueRequest represents the PATCH /api/issues/:id request body.
// All fields are optional pointers to support partial updates.
// Field set matches the current PatchIssueRequest in handlers_issues.go.
type PatchIssueRequest struct {
	Title              *string  `json:"title,omitempty"`
	Description        *string  `json:"description,omitempty"`
	Status             *string  `json:"status,omitempty"`
	Priority           *int     `json:"priority,omitempty"`
	Assignee           *string  `json:"assignee,omitempty"`
	Owner              *string  `json:"owner,omitempty"`
	Design             *string  `json:"design,omitempty"`
	AcceptanceCriteria *string  `json:"acceptance_criteria,omitempty"`
	Notes              *string  `json:"notes,omitempty"`
	ExternalRef        *string  `json:"external_ref,omitempty"`
	EstimatedMinutes   *int     `json:"estimated_minutes,omitempty"`
	IssueType          *string  `json:"issue_type,omitempty"`
	AddLabels          []string `json:"add_labels,omitempty"`
	RemoveLabels       []string `json:"remove_labels,omitempty"`
	SetLabels          []string `json:"set_labels,omitempty"`
	Pinned             *bool    `json:"pinned,omitempty"`
	Parent             *string  `json:"parent,omitempty"`
	DueAt              *string  `json:"due_at,omitempty"`
	DeferUntil         *string  `json:"defer_until,omitempty"`
	AgentState         *string  `json:"agent_state,omitempty"`
}
