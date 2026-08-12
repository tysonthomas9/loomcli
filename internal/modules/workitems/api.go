package workitems

import "context"

// API is the Work Items-owned lifecycle, comment, event, and dependency
// surface. Commands use aggregate-specific inputs; callers never receive a
// generic issue updater.
type API interface {
	Create(context.Context, CreateCommand) (*CreatedIssue, error)
	List(context.Context, ListQuery) (*ListResult, error)
	Ready(context.Context, AvailabilityQuery) ([]IssueSummary, error)
	Blocked(context.Context, AvailabilityQuery) ([]IssueSummary, error)
	Search(context.Context, SearchQuery) ([]IssueSummary, error)
	Get(context.Context, GetQuery) (*IssueDetail, error)
	Patch(context.Context, PatchCommand) (*IssueDetail, error)
	Close(context.Context, CloseCommand) (*CloseResult, error)
	Claim(context.Context, ClaimCommand) (*IssueDetail, error)
	Reopen(context.Context, ReopenCommand) error
	BlockRepositoryRequired(context.Context, BlockRepositoryRequiredCommand) (*RepositoryAdmissionResult, error)
	AssignRepository(context.Context, AssignRepositoryCommand) (*IssueSummary, error)
	Delete(context.Context, DeleteCommand) (DeleteResult, error)
	ListEvents(context.Context, ListEventsQuery) ([]*Event, error)
	AddComment(context.Context, AddCommentCommand) (*Comment, error)
	ListComments(context.Context, ListCommentsQuery) ([]*Comment, error)
	AddDependency(context.Context, AddDependencyCommand) error
	RemoveDependency(context.Context, RemoveDependencyCommand) error
	ListDependencies(context.Context, ListDependenciesQuery) ([]Dependency, error)
}

// ReadyQueries is the narrow queue projection used by schedulers and the
// ready-list HTTP adapter. It does not expose mutation authority.
type ReadyQueries interface {
	Ready(context.Context, AvailabilityQuery) ([]IssueSummary, error)
}

// StatsQueries is the narrow aggregate projection consumed by health and
// readiness delivery adapters.
type StatsQueries interface {
	Stats(context.Context) (*Stats, error)
}

// BlockedQueries is the narrow blocked-work projection consumed by delivery
// adapters. It does not force unrelated Work Items consumers to depend on
// queue availability or mutation methods.
type BlockedQueries interface {
	Blocked(context.Context, AvailabilityQuery) ([]IssueSummary, error)
}

type ListQuery struct {
	Filter         ListFilter
	ExcludeStatus  []string
	IncludeBlocked bool
}

type ListFilter struct {
	Query               string
	ExternalRef         string
	Status              string
	Priority            *int
	IssueType           string
	Assignee            string
	Labels              []string
	LabelsAny           []string
	SourceRepos         []string
	Limit               int
	TitleContains       string
	DescriptionContains string
	NotesContains       string
	CreatedAfter        string
	CreatedBefore       string
	UpdatedAfter        string
	UpdatedBefore       string
	EmptyDescription    bool
	NoAssignee          bool
	NoLabels            bool
	Pinned              *bool
	ParentID            string
	Lightweight         bool
}

type AvailabilityQuery struct {
	ParentID    string
	Assignee    string
	Unassigned  bool
	Priority    *int
	IssueType   string
	Labels      []string
	LabelsAny   []string
	SourceRepos []string
	Limit       int
	SortPolicy  string
	MolType     string
}

type CreateCommand struct {
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
	IdempotencyKey     string
	Force              bool
}

type SearchQuery struct {
	Query string
	Limit int
}

type GetQuery struct {
	IssueID string
}

type PatchCommand struct {
	IssueID            string
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
	AddLabels          []string
	RemoveLabels       []string
	SetLabels          []string
	Pinned             *bool
	Parent             *string
	DueAt              *string
	DeferUntil         *string
	AgentState         *string
}

type CloseCommand struct {
	IssueID     string
	Reason      string
	Session     string
	SuggestNext bool
	Force       bool
}

type ClaimCommand struct {
	IssueID string
}

type ReopenCommand struct {
	IssueID string
	Reason  string
}

// BlockRepositoryRequiredCommand performs the Work Items-owned atomic
// repository-admission transition. Callers cannot mint the reserved block
// metadata through the generic Patch command.
type BlockRepositoryRequiredCommand struct {
	IssueID string
}

type AssignRepositoryCommand struct {
	IssueID    string
	Repository string
}

type DeleteCommand struct {
	IssueID string
}

type ListEventsQuery struct {
	IssueID string
	Limit   int
}

type AddCommentCommand struct {
	IssueID string
	Author  string
	Text    string
}

type ListCommentsQuery struct {
	IssueID string
}

type AddDependencyCommand struct {
	IssueID     string
	DependsOnID string
	Type        string
}

type RemoveDependencyCommand struct {
	IssueID     string
	DependsOnID string
	Type        string
}

type ListDependenciesQuery struct {
	IssueID string
}
