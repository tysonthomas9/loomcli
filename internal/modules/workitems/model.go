package workitems

import "time"

// Comment is the Work Items-owned immutable comment projection.
type Comment struct {
	ID        int64     `json:"id"`
	IssueID   string    `json:"issue_id"`
	Author    string    `json:"author"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// Dependency is the Work Items-owned UI projection for one related item.
type Dependency struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Status         string    `json:"status"`
	Priority       int       `json:"priority"`
	CreatedAt      time.Time `json:"created_at"`
	DependencyType string    `json:"dependency_type"`
	IssueType      string    `json:"issue_type,omitempty"`
	CreatedBy      string    `json:"created_by,omitempty"`
}
