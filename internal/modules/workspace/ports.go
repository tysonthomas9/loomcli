package workspace

import "context"

// CatalogStore is the Workspace-owned durable query port.
type CatalogStore interface {
	GetByKey(context.Context, string) (*Reference, error)
	GetByName(context.Context, string) (*Reference, error)
	List(context.Context) ([]Reference, error)
	Rename(context.Context, string, string) (*Reference, error)
	SetDesignFormat(context.Context, string, string) (*Reference, error)
}

// RepositoryCatalogStore is the Workspace-owned durable repository query
// port. It contains shared catalog data only, never local checkout state.
type RepositoryCatalogStore interface {
	Get(context.Context, string, string) (*Repository, error)
	List(context.Context, string) ([]Repository, error)
}
