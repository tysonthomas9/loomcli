package cli

import "time"

// migrateIssue is a slim issue record from `bd list --json`.
type migrateIssue struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Priority  int    `json:"priority"`
	IssueType string `json:"issue_type"`
	Parent    string `json:"parent,omitempty"`
}

// migrateIssueDetail is the full issue from `bd show <id> --json`.
type migrateIssueDetail struct {
	ID                 string              `json:"id"`
	Title              string              `json:"title"`
	Status             string              `json:"status"`
	Priority           int                 `json:"priority"`
	IssueType          string              `json:"issue_type"`
	Parent             string              `json:"parent,omitempty"`
	Description        string              `json:"description,omitempty"`
	Design             string              `json:"design,omitempty"`
	AcceptanceCriteria string              `json:"acceptance_criteria,omitempty"`
	Notes              string              `json:"notes,omitempty"`
	Assignee           string              `json:"assignee,omitempty"`
	Owner              string              `json:"owner,omitempty"`
	CreatedBy          string              `json:"created_by,omitempty"`
	ExternalRef        string              `json:"external_ref,omitempty"`
	EstimatedMinutes   *int                `json:"estimated_minutes,omitempty"`
	Labels             []string            `json:"labels,omitempty"`
	Dependencies       []migrateDependency `json:"dependencies,omitempty"`
	Dependents         []migrateDependency `json:"dependents,omitempty"`
	Comments           []migrateComment    `json:"comments,omitempty"`
	DueAt              *time.Time          `json:"due_at,omitempty"`
	DeferUntil         *time.Time          `json:"defer_until,omitempty"`
	ClosedAt           *time.Time          `json:"closed_at,omitempty"`
	CloseReason        string              `json:"close_reason,omitempty"`
}

// migrateDependency is a parsed dependency from `bd show --json`.
type migrateDependency struct {
	IssueID     string `json:"issue_id,omitempty"`
	DependsOnID string `json:"depends_on_id,omitempty"`
	Type        string `json:"type,omitempty"`
	// dependency_type is used in the dependents array (parent-child vs blocks).
	DependencyType string `json:"dependency_type,omitempty"`
}

// migrateComment is a parsed comment from `bd show --json`.
type migrateComment struct {
	Author    string `json:"author,omitempty"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at,omitempty"`
}

// migrateConfig holds resolved migration parameters.
type migrateConfig struct {
	fleetURL      string
	workspace     string
	apiKey        string
	dryRun        bool
	includeClosed bool
	batchSize     int
	updateConfig  bool
	projectDir    string
}

// migrateResult tracks counts and errors for the migration report.
type migrateResult struct {
	created       int
	skipped       int
	failed        int
	depsAdded     int
	depsSkipped   int
	commentsAdded int
	errors        []string
}
