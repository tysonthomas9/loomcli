package workitems

import "time"

// IssueSummary is the Work Items-owned list/search projection. Repo mirrors
// SourceRepo for the existing Web UI wire contract while callers migrate.
type IssueSummary struct {
	ID               string     `json:"id"`
	Title            string     `json:"title"`
	Status           string     `json:"status"`
	Priority         int        `json:"priority"`
	IssueType        string     `json:"issue_type,omitempty"`
	Assignee         string     `json:"assignee,omitempty"`
	Owner            string     `json:"owner,omitempty"`
	Labels           []string   `json:"labels,omitempty"`
	SourceRepo       string     `json:"source_repo,omitempty"`
	Repo             string     `json:"repo,omitempty"`
	Parent           string     `json:"parent,omitempty"`
	Design           string     `json:"design,omitempty"`
	DesignArtifactID string     `json:"design_artifact_id,omitempty"`
	DesignFormat     string     `json:"design_format,omitempty"`
	HasDesign        bool       `json:"has_design"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	DueAt            *time.Time `json:"due_at,omitempty"`
	DeferUntil       *time.Time `json:"defer_until,omitempty"`
}

// IssueDetail is the stable Work Items aggregate projection returned after an
// owner command such as Claim. Relationship slices are never omitted so the
// existing UI receives a stable shape.
type IssueDetail struct {
	ID                 string       `json:"id"`
	Title              string       `json:"title"`
	Status             string       `json:"status"`
	Priority           int          `json:"priority"`
	IssueType          string       `json:"issue_type,omitempty"`
	Assignee           string       `json:"assignee,omitempty"`
	Owner              string       `json:"owner,omitempty"`
	Labels             []string     `json:"labels"`
	SourceRepo         string       `json:"source_repo,omitempty"`
	Repo               string       `json:"repo,omitempty"`
	Parent             string       `json:"parent,omitempty"`
	Design             string       `json:"design,omitempty"`
	DesignArtifactID   string       `json:"design_artifact_id,omitempty"`
	DesignFormat       string       `json:"design_format,omitempty"`
	HasDesign          bool         `json:"has_design"`
	Description        string       `json:"description,omitempty"`
	AcceptanceCriteria string       `json:"acceptance_criteria,omitempty"`
	Notes              string       `json:"notes,omitempty"`
	CreatedBy          string       `json:"created_by,omitempty"`
	CreatedAt          time.Time    `json:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at"`
	ClosedAt           *time.Time   `json:"closed_at,omitempty"`
	CloseReason        string       `json:"close_reason,omitempty"`
	ClosedBySession    string       `json:"closed_by_session,omitempty"`
	ExternalRef        string       `json:"external_ref,omitempty"`
	EstimatedMinutes   *int         `json:"estimated_minutes,omitempty"`
	DueAt              *time.Time   `json:"due_at,omitempty"`
	DeferUntil         *time.Time   `json:"defer_until,omitempty"`
	Dependencies       []Dependency `json:"dependencies"`
	Dependents         []Dependency `json:"dependents"`
	Comments           []*Comment   `json:"comments"`
}

type DeleteResult struct {
	DeletedCount int      `json:"deleted_count"`
	DeletedIDs   []string `json:"deleted_ids"`
}

type Event struct {
	ID        int64     `json:"id"`
	IssueID   string    `json:"issue_id"`
	EventType EventType `json:"event_type"`
	Actor     string    `json:"actor"`
	OldValue  *string   `json:"old_value,omitempty"`
	NewValue  *string   `json:"new_value,omitempty"`
	Comment   *string   `json:"comment,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type EventType string

const (
	EventCreated           EventType = "issue.created"
	EventUpdated           EventType = "issue.updated"
	EventStatusChanged     EventType = "issue.status_changed"
	EventCommented         EventType = "issue.commented"
	EventClosed            EventType = "issue.closed"
	EventReopened          EventType = "issue.reopened"
	EventDependencyAdded   EventType = "issue.dependency_added"
	EventDependencyRemoved EventType = "issue.dependency_removed"
	EventLabelAdded        EventType = "issue.label_added"
	EventLabelRemoved      EventType = "issue.label_removed"
	EventCompacted         EventType = "issue.compacted"
)

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
