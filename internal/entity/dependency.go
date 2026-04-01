package entity

import "time"

// Dependency represents a relationship between issues.
type Dependency struct {
	IssueID     string         `json:"issue_id"`
	DependsOnID string         `json:"depends_on_id"`
	Type        DependencyType `json:"type"`
	CreatedAt   time.Time      `json:"created_at"`
	CreatedBy   string         `json:"created_by,omitempty"`
	Metadata    string         `json:"metadata,omitempty"`
	ThreadID    string         `json:"thread_id,omitempty"`
}

// DependencyType categorizes the relationship between issues.
type DependencyType string

// IsValid checks if the dependency type value is valid.
// Accepts any non-empty string up to 50 characters.
func (d DependencyType) IsValid() bool {
	return len(d) > 0 && len(d) <= 50
}
