package workitems

import (
	"encoding/json"
	"time"
)

// CreatedIssue preserves create's durable-success behavior: a canonical full
// projection is preferred, while Summary remains a valid fallback when the
// post-create read is unavailable. Its JSON representation is the selected
// issue itself, not a migration wrapper.
type CreatedIssue struct {
	Detail  *IssueDetail
	Summary *IssueSummary
}

func (r CreatedIssue) MarshalJSON() ([]byte, error) {
	if r.Detail != nil {
		return json.Marshal(r.Detail)
	}
	return json.Marshal(r.Summary)
}

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
	Notes            string     `json:"notes,omitempty"`
	CreatedBy        string     `json:"created_by,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	CloseReason      string     `json:"close_reason,omitempty"`
	ExternalRef      string     `json:"external_ref,omitempty"`
	DueAt            *time.Time `json:"due_at,omitempty"`
	DeferUntil       *time.Time `json:"defer_until,omitempty"`
	DependencyCount  int        `json:"dependency_count"`
	DependentCount   int        `json:"dependent_count"`
	BlockedByCount   int        `json:"blocked_by_count,omitempty"`
	BlockedBy        []string   `json:"blocked_by,omitempty"`
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

type CloseResult struct {
	Closed    *IssueSummary  `json:"closed"`
	Unblocked []IssueSummary `json:"unblocked"`
}

type RepositoryAdmissionResult struct {
	Issue         *IssueSummary `json:"issue,omitempty"`
	Changed       bool          `json:"changed"`
	Replayed      bool          `json:"replayed"`
	DispatchReady bool          `json:"dispatch_ready"`
	Blocked       bool          `json:"blocked,omitempty"`
	Reopened      bool          `json:"reopened,omitempty"`
	Outcome       string        `json:"outcome,omitempty"`
}

type ListResult struct {
	Issues       []ListItem
	KanbanIssues []KanbanItem
}

type ListItem struct {
	IssueSummary
	ParentTitle *string `json:"parent_title,omitempty"`
}

type KanbanItem struct {
	IssueSummary
	ParentTitle    *string  `json:"parent_title,omitempty"`
	IsBlocked      bool     `json:"is_blocked"`
	IsReady        bool     `json:"is_ready"`
	IsDeferred     bool     `json:"is_deferred"`
	BlockedByCount int      `json:"blocked_by_count"`
	BlockedBy      []string `json:"blocked_by,omitempty"`
}

// Stats is the Work Items-owned aggregate statistics projection.
type Stats struct {
	TotalIssues             int     `json:"total_issues"`
	OpenIssues              int     `json:"open_issues"`
	InProgressIssues        int     `json:"in_progress_issues"`
	ClosedIssues            int     `json:"closed_issues"`
	BlockedIssues           int     `json:"blocked_issues"`
	DeferredIssues          int     `json:"deferred_issues"`
	ReadyIssues             int     `json:"ready_issues"`
	TombstoneIssues         int     `json:"tombstone_issues"`
	PinnedIssues            int     `json:"pinned_issues"`
	EpicsEligibleForClosure int     `json:"epics_eligible_for_closure"`
	AverageLeadTime         float64 `json:"average_lead_time_hours"`
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

// Mutation is the durable Work Items change-stream projection consumed by
// realtime delivery. Cursor is opaque and owned by FleetDB; callers must pass
// it back unchanged rather than deriving a timestamp cursor.
type Mutation struct {
	Cursor     string    `json:"cursor,omitempty"`
	Type       string    `json:"type"`
	EntityType string    `json:"entity_type,omitempty"`
	EntityID   string    `json:"entity_id,omitempty"`
	Action     string    `json:"action,omitempty"`
	IssueID    string    `json:"issue_id,omitempty"`
	Title      string    `json:"title,omitempty"`
	Assignee   string    `json:"assignee,omitempty"`
	Actor      string    `json:"actor,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
	OldStatus  string    `json:"old_status,omitempty"`
	NewStatus  string    `json:"new_status,omitempty"`
	ParentID   string    `json:"parent_id,omitempty"`
	SourceRepo string    `json:"source_repo,omitempty"`
	StepCount  int       `json:"step_count,omitempty"`
}

const (
	MutationCreate        = "create"
	MutationUpdate        = "update"
	MutationDelete        = "delete"
	MutationComment       = "comment"
	MutationBonded        = "bonded"
	MutationSquashed      = "squashed"
	MutationBurned        = "burned"
	MutationStatus        = "status"
	MutationRefresh       = "refresh"
	MutationSessionChange = "session_change"
)

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
