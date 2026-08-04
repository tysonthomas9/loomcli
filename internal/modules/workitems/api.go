package workitems

import "context"

// API is the Work Items-owned lifecycle, comment, event, and dependency
// surface. Commands use aggregate-specific inputs; callers never receive a
// generic issue updater.
type API interface {
	Search(context.Context, SearchQuery) ([]IssueSummary, error)
	Get(context.Context, GetQuery) (*IssueDetail, error)
	Claim(context.Context, ClaimCommand) (*IssueDetail, error)
	Reopen(context.Context, ReopenCommand) error
	Delete(context.Context, DeleteCommand) (DeleteResult, error)
	ListEvents(context.Context, ListEventsQuery) ([]*Event, error)
	AddComment(context.Context, AddCommentCommand) (*Comment, error)
	ListComments(context.Context, ListCommentsQuery) ([]*Comment, error)
	AddDependency(context.Context, AddDependencyCommand) error
	RemoveDependency(context.Context, RemoveDependencyCommand) error
	ListDependencies(context.Context, ListDependenciesQuery) ([]Dependency, error)
}

type SearchQuery struct {
	Query string
	Limit int
}

type GetQuery struct {
	IssueID string
}

type ClaimCommand struct {
	IssueID string
}

type ReopenCommand struct {
	IssueID string
	Reason  string
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
