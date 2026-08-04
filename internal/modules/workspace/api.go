package workspace

import "context"

// API is the Workspace-owned catalog surface. Repository materialization and
// Git mechanics remain Source Control responsibilities.
type API interface {
	Resolve(context.Context, ResolveQuery) (*Reference, error)
	List(context.Context, ListQuery) ([]Reference, error)
	Rename(context.Context, RenameCommand) (*Reference, error)
	SetDesignFormat(context.Context, SetDesignFormatCommand) (*Reference, error)
}

type ResolveQuery struct {
	Reference string
}

// ListQuery is intentionally empty today. It is a named query so future
// filtering can evolve without widening the public method signature.
type ListQuery struct{}

type RenameCommand struct {
	Reference string
	Name      string
}

type SetDesignFormatCommand struct {
	Reference string
	Format    string
}
