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
