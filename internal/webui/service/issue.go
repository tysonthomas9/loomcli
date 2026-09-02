package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// ListIssuesParams holds the parsed parameters for listing issues.
type ListIssuesParams struct {
	Args           *rpc.ListArgs
	ExcludeStatus  []string
	IncludeBlocked bool
}

// ListIssuesResult holds the result of listing issues.
// Exactly one of Issues or KanbanIssues is non-nil.
type ListIssuesResult struct {
	Issues       []IssueWithParent // set when IncludeBlocked is false
	KanbanIssues []KanbanIssue     // set when IncludeBlocked is true
}

// IssueWithParent extends IssueWithCounts with parent info.
type IssueWithParent struct {
	*types.IssueWithCounts
	Parent      *string `json:"parent,omitempty"`
	ParentTitle *string `json:"parent_title,omitempty"`
	Repo        *string `json:"repo,omitempty"`
}

// KanbanIssue extends IssueWithParent with blocked dependency info.
type KanbanIssue struct {
	*types.IssueWithCounts
	Parent           *string            `json:"parent,omitempty"`
	ParentTitle      *string            `json:"parent_title,omitempty"`
	IsBlocked        bool               `json:"is_blocked"`
	IsReady          bool               `json:"is_ready"`
	IsDeferred       bool               `json:"is_deferred"`
	BlockedByCount   int                `json:"blocked_by_count"`
	BlockedBy        []string           `json:"blocked_by,omitempty"`
	BlockedByDetails []types.BlockerRef `json:"blocked_by_details,omitempty"`
	Repo             *string            `json:"repo,omitempty"`
}

// WorkspaceValidator validates and resolves workspace targets for move operations.
// Defined here to avoid an import cycle with the webui package.
type WorkspaceValidator interface {
	ValidateTarget(targetWorkspace string) (targetWsID string, err error)
	CurrentWorkspace() string
}

// MoveIssueParams holds the parameters for moving an issue.
type MoveIssueParams struct {
	IssueID         string
	TargetWorkspace string
	Validator       WorkspaceValidator
}

// MoveIssueResult holds the result of a move operation.
type MoveIssueResult struct {
	SourceID string
	TargetID string
	Warnings []string
}

// CloseIssueParams holds the parameters for closing an issue.
type CloseIssueParams struct {
	IssueID     string
	Actor       string
	Reason      string
	Session     string
	SuggestNext bool
	Force       bool
}

// ClaimIssueParams holds the parameters for atomically claiming an issue.
type ClaimIssueParams struct {
	IssueID string
}

// CreateIssueParams mirrors IssueCreateRequest but is not HTTP-bound.
type CreateIssueParams struct {
	Title              string
	IssueType          string
	Priority           int
	ID                 string
	Parent             string
	Description        string
	Status             string
	Design             string
	AcceptanceCriteria string
	Notes              string
	Assignee           string
	Owner              string
	CreatedBy          string
	ExternalRef        string
	EstimatedMinutes   *int
	Labels             []string
	Dependencies       []string
	DueAt              string
	DeferUntil         string
	SourceRepo         string
	// IdempotencyKey / Force arrive as X-Idempotency-Key / X-Idempotency-Force
	// request headers and are forwarded header-only to fleet-db (its strict
	// JSON decode rejects unknown body fields).
	IdempotencyKey string
	Force          bool
}

// PatchIssueParams mirrors PatchIssueRequest but is not HTTP-bound.
type PatchIssueParams struct {
	IssueID            string
	Actor              string
	Title              *string
	Description        *string
	Status             *string
	Priority           *int
	Assignee           *string
	Owner              *string
	Design             *string
	DesignFormat       *string
	AcceptanceCriteria *string
	Notes              *string
	ExternalRef        *string
	EstimatedMinutes   *int
	IssueType          *string
	Repo               *string
	AddLabels          []string
	RemoveLabels       []string
	SetLabels          []string
	Pinned             *bool
	Parent             *string
	DueAt              *string
	DeferUntil         *string
	AgentState         *string
}

// AddCommentParams holds the parameters for adding a comment.
type AddCommentParams struct {
	IssueID string
	Actor   string
	Author  string
	Text    string
}

// AddDependencyParams holds the parameters for adding a dependency.
type AddDependencyParams struct {
	IssueID     string
	Actor       string
	DependsOnID string
	DepType     string // defaults to "blocks"
}

// RemoveDependencyParams holds the parameters for removing a dependency.
type RemoveDependencyParams struct {
	IssueID string
	Actor   string
	DepID   string
}

// EventListParams holds the parameters for listing events.
type EventListParams struct {
	IssueID string
	Limit   int
	Since   *string
}

// EventListResult carries issue events and completeness metadata to the HTTP
// handler without exposing backend wire types.
type EventListResult struct {
	Events []*types.Event
	// Cursor is set only for a forward Since page. Newest-tail responses omit
	// it; callers start a separate forward walk with an empty Since cursor.
	Cursor  string
	HasMore bool
	// TotalEvents is zero when the backend cannot report an exact history size.
	TotalEvents int
}

// Journey describes the durable server-side reconstruction of an issue's
// stage history and any host-local agent work that can be overlaid on it.
type Journey struct {
	Spans        []JourneySpan        `json:"spans"`
	AgentWindows []JourneyAgentWindow `json:"agent_windows"`
	LeadTime     JourneyLeadTime      `json:"lead_time"`
	Honesty      JourneyHonesty       `json:"honesty"`
}

// JourneySpan is one contiguous status/owner/needs-revision state. End is nil
// only for the final span of an in-flight issue.
type JourneySpan struct {
	Kind          string     `json:"kind"`
	Stage         string     `json:"stage"`
	Start         time.Time  `json:"start"`
	End           *time.Time `json:"end"`
	Owner         *string    `json:"owner"`
	Actor         *string    `json:"actor"`
	NeedsRevision bool       `json:"needs_revision"`
	Stalled       bool       `json:"stalled"`
	Approximate   bool       `json:"approximate"`
	UnknownStart  bool       `json:"unknown_start"`
}

// JourneyAgentWindow is one host-local task attempt from claim to terminal
// lifecycle event. End remains nil and outcome is running for an active attempt.
// A later claim supersedes an unclosed earlier attempt, recording outcome
// superseded at the later claim timestamp.
type JourneyAgentWindow struct {
	TaskID  string     `json:"task_id"`
	Agent   string     `json:"agent"`
	Start   time.Time  `json:"start"`
	End     *time.Time `json:"end"`
	Outcome string     `json:"outcome"`
}

// JourneyLeadTime reports millisecond durations so the wire unit is explicit.
// TotalMS is visible wall-clock lead time. The four named buckets deliberately
// may sum below it: deferred/open-assigned intervals and gaps between exact
// lifecycle attempts do not satisfy any settled category and are not forced
// into a confident but false classification.
type JourneyLeadTime struct {
	TotalMS             int64 `json:"total_ms"`
	QueuedMS            int64 `json:"queued_ms"`
	AgentWorkingMS      int64 `json:"agent_working_ms"`
	WaitingOnOperatorMS int64 `json:"waiting_on_operator_ms"`
	HaltedMS            int64 `json:"halted_ms"`
}

// JourneyHonesty makes bounded issue history and missing host-local overlays
// explicit instead of presenting a partial fold as authoritative.
type JourneyHonesty struct {
	CompleteHistory       bool   `json:"complete_history"`
	Bounded               bool   `json:"bounded"`
	HasMore               bool   `json:"has_more"`
	EventsSeen            int    `json:"events_seen"`
	TotalEvents           int    `json:"total_events,omitempty"`
	Reason                string `json:"reason,omitempty"`
	AgentWindowsAvailable bool   `json:"agent_windows_available"`
	AgentWindowsReason    string `json:"agent_windows_reason,omitempty"`
}

// SearchIssuesParams holds the parameters for full-text search.
type SearchIssuesParams struct {
	Query string
	Limit int
}

// ReopenIssueParams holds the parameters for reopening a closed issue.
type ReopenIssueParams struct {
	IssueID string
	Actor   string
	Reason  string
}

// IssueService defines the business logic operations for issues.
type IssueService interface {
	GetIssue(ctx context.Context, issueID string) (json.RawMessage, error)
	ListIssues(ctx context.Context, params ListIssuesParams) (*ListIssuesResult, error)
	CreateIssue(ctx context.Context, params CreateIssueParams) (json.RawMessage, error)
	PatchIssue(ctx context.Context, params PatchIssueParams) error
	CloseIssue(ctx context.Context, params CloseIssueParams) (json.RawMessage, error)
	ReopenIssue(ctx context.Context, params ReopenIssueParams) error
	ClaimIssue(ctx context.Context, params ClaimIssueParams) (json.RawMessage, error)
	DeleteIssue(ctx context.Context, issueID string) (json.RawMessage, error)
	AddComment(ctx context.Context, params AddCommentParams) (*types.Comment, error)
	ListComments(ctx context.Context, issueID string) ([]*types.Comment, error)
	AddDependency(ctx context.Context, params AddDependencyParams) error
	RemoveDependency(ctx context.Context, params RemoveDependencyParams) error
	ListDependencies(ctx context.Context, issueID string) (json.RawMessage, error)
	ListEvents(ctx context.Context, params EventListParams) ([]*types.Event, error)
	ListEventHistory(ctx context.Context, params EventListParams) (*EventListResult, error)
	GetJourney(ctx context.Context, issueID string) (*Journey, error)
	MoveIssue(ctx context.Context, params MoveIssueParams) (*MoveIssueResult, error)
	SearchIssues(ctx context.Context, params SearchIssuesParams) (json.RawMessage, error)
}
