package workspace

import "context"

// CatalogStore is the Workspace-owned durable query port.
type CatalogStore interface {
	GetByKey(context.Context, string) (*Reference, error)
	GetByName(context.Context, string) (*Reference, error)
}
