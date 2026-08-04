package workitems

import "context"

// Store is the consumer-owned durable port for the first Work Items slice.
// It exposes semantic comment and dependency operations instead of generic
// CRUD or a process-wide Store.
type Store interface {
	AddComment(context.Context, AddCommentCommand) (*Comment, error)
	ListComments(context.Context, ListCommentsQuery) ([]*Comment, error)
	AddDependency(context.Context, AddDependencyCommand) error
	RemoveDependency(context.Context, RemoveDependencyCommand) error
	ListDependencies(context.Context, ListDependenciesQuery) ([]Dependency, error)
}
