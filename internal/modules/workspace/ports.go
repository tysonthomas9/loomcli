package workspace

import "context"

// CatalogStore is the Workspace-owned durable query port.
type CatalogStore interface {
	Create(context.Context, CreateInput) (*Reference, error)
	GetByKey(context.Context, string) (*Reference, error)
	GetByName(context.Context, string) (*Reference, error)
	List(context.Context) ([]Reference, error)
	Rename(context.Context, string, string) (*Reference, error)
	SetDesignFormat(context.Context, string, string) (*Reference, error)
	SetLifecycle(context.Context, string, LifecycleUpdate) (*Reference, error)
	Delete(context.Context, string) error
}

type CreateInput struct {
	Key           string
	Name          string
	Description   string
	DefaultBranch string
	DesignFormat  string
}

type LifecycleUpdate struct {
	State         State
	ErrorMessage  string
	DefaultBranch *string
}

// RepositoryCatalogStore is the Workspace-owned durable repository query
// port. It contains shared catalog data only, never local checkout state.
type RepositoryCatalogStore interface {
	Create(context.Context, RepositoryInput) (*Repository, error)
	Get(context.Context, string, string) (*Repository, error)
	List(context.Context, string) ([]Repository, error)
	Update(context.Context, string, string, RepositoryUpdate) (*Repository, error)
	Delete(context.Context, string, string) error
}

type RepositoryInput struct {
	WorkspaceKey  string
	Name          string
	RemoteURL     string
	Remote        string
	DefaultBranch string
	Groups        []string
	SourceRepoID  string
}

type RepositoryUpdate struct {
	RemoteURL     *string
	Remote        *string
	DefaultBranch *string
	Groups        *[]string
	SourceRepoID  *string
}
