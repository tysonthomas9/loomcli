package workitems

import "context"

// API is the Work Items-owned comment and dependency surface. Commands use
// aggregate-specific inputs; callers never receive a generic issue updater.
type API interface {
	AddComment(context.Context, AddCommentCommand) (*Comment, error)
	ListComments(context.Context, ListCommentsQuery) ([]*Comment, error)
	AddDependency(context.Context, AddDependencyCommand) error
	RemoveDependency(context.Context, RemoveDependencyCommand) error
	ListDependencies(context.Context, ListDependenciesQuery) ([]Dependency, error)
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
