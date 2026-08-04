package workspace

import "context"

// API is the Workspace-owned catalog surface. Repository materialization and
// Git mechanics remain Source Control responsibilities.
type API interface {
	Resolve(context.Context, ResolveQuery) (*Reference, error)
}

type ResolveQuery struct {
	Reference string
}
