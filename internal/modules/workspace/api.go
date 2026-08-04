package workspace

import "context"

// API is the Workspace-owned catalog surface. Repository materialization and
// Git mechanics remain Source Control responsibilities.
type API interface {
	Create(context.Context, CreateCommand) (*Reference, error)
	Resolve(context.Context, ResolveQuery) (*Reference, error)
	List(context.Context, ListQuery) ([]Reference, error)
	Rename(context.Context, RenameCommand) (*Reference, error)
	SetDesignFormat(context.Context, SetDesignFormatCommand) (*Reference, error)
	SetLifecycle(context.Context, SetLifecycleCommand) (*Reference, error)
	Delete(context.Context, DeleteCommand) (*Reference, error)
	GetRepository(context.Context, GetRepositoryQuery) (*Repository, error)
	ListRepositories(context.Context, ListRepositoriesQuery) ([]Repository, error)
	RegisterRepository(context.Context, RegisterRepositoryCommand) (*Repository, error)
	UpdateRepository(context.Context, UpdateRepositoryCommand) (*Repository, error)
	UnregisterRepository(context.Context, UnregisterRepositoryCommand) (*Repository, error)
}

type CreateCommand struct {
	Key           string
	Name          string
	Description   string
	DefaultBranch string
	DesignFormat  string
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

// SetLifecycleCommand owns the durable Workspace lifecycle projection. A nil
// DefaultBranch preserves the current value; a non-nil empty value clears it.
type SetLifecycleCommand struct {
	Reference     string
	State         State
	ErrorMessage  string
	DefaultBranch *string
}

type DeleteCommand struct {
	Reference string
}

type GetRepositoryQuery struct {
	WorkspaceReference string
	Name               string
}

type ListRepositoriesQuery struct {
	WorkspaceReference string
}

type RegisterRepositoryCommand struct {
	WorkspaceReference string
	Name               string
	RemoteURL          string
	Remote             string
	DefaultBranch      string
	Groups             []string
	SourceRepoID       string
}

// UpdateRepositoryCommand applies a partial update to Workspace-owned shared
// repository catalog state. Nil fields preserve the current value; local path,
// checkout, and worktree state deliberately remain outside this command.
type UpdateRepositoryCommand struct {
	WorkspaceReference string
	Name               string
	RemoteURL          *string
	Remote             *string
	DefaultBranch      *string
	Groups             *[]string
	SourceRepoID       *string
}

type UnregisterRepositoryCommand struct {
	WorkspaceReference string
	Name               string
}
