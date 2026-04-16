package service

import (
	"context"
	"encoding/json"

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
}

// PatchIssueParams mirrors PatchIssueRequest but is not HTTP-bound.
type PatchIssueParams struct {
	IssueID            string
	Title              *string
	Description        *string
	Status             *string
	Priority           *int
	Assignee           *string
	Owner              *string
	Design             *string
	AcceptanceCriteria *string
	Notes              *string
	ExternalRef        *string
	EstimatedMinutes   *int
	IssueType          *string
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
	Author  string
	Text    string
}

// AddDependencyParams holds the parameters for adding a dependency.
type AddDependencyParams struct {
	IssueID     string
	DependsOnID string
	DepType     string // defaults to "blocks"
}

// RemoveDependencyParams holds the parameters for removing a dependency.
type RemoveDependencyParams struct {
	IssueID string
	DepID   string
}

// EventListParams holds the parameters for listing events.
type EventListParams struct {
	IssueID string
	Limit   int
}

// IssueService defines the business logic operations for issues.
type IssueService interface {
	GetIssue(ctx context.Context, issueID string) (json.RawMessage, error)
	ListIssues(ctx context.Context, params ListIssuesParams) (*ListIssuesResult, error)
	CreateIssue(ctx context.Context, params CreateIssueParams) (json.RawMessage, error)
	PatchIssue(ctx context.Context, params PatchIssueParams) error
	CloseIssue(ctx context.Context, params CloseIssueParams) (json.RawMessage, error)
	ClaimIssue(ctx context.Context, params ClaimIssueParams) (json.RawMessage, error)
	DeleteIssue(ctx context.Context, issueID string) (json.RawMessage, error)
	AddComment(ctx context.Context, params AddCommentParams) (*types.Comment, error)
	AddDependency(ctx context.Context, params AddDependencyParams) error
	RemoveDependency(ctx context.Context, params RemoveDependencyParams) error
	ListEvents(ctx context.Context, params EventListParams) ([]*types.Event, error)
	MoveIssue(ctx context.Context, params MoveIssueParams) (*MoveIssueResult, error)
}
