package workspace

import "context"

// WorkspaceCreate is the input for WorkspaceStore.Create. Required: Key
// (must satisfy fleet-db's `^[A-Z]([A-Z0-9-]{0,30}[A-Z0-9])?$` regex)
// and Name. Other fields are optional and apply server-side defaults.
type WorkspaceCreate struct {
	Key           string
	Name          string
	Description   string
	DefaultBranch string
	DesignFormat  string
}

// WorkspaceUpdate is a partial-update payload. Only non-nil fields are
// applied. Pointer types distinguish "unset" from "set to zero value".
type WorkspaceUpdate struct {
	Name          *string
	Description   *string
	DefaultBranch *string
	DesignFormat  *string
	State         *State
	ErrorMessage  *string
}

// WorkspaceStore is the persistence interface for Workspace entities.
type WorkspaceStore interface {
	// Create inserts a new workspace. Returns ErrAlreadyExists if Key
	// already exists, ErrInvalid for malformed input.
	Create(ctx context.Context, in WorkspaceCreate) (*Workspace, error)

	// Get returns the workspace by Key. Returns ErrNotFound if absent.
	Get(ctx context.Context, key string) (*Workspace, error)

	// GetByName returns the workspace whose Name matches. Returns
	// ErrNotFound if no such workspace; behavior is undefined if names
	// are non-unique (callers should treat this as best-effort).
	GetByName(ctx context.Context, name string) (*Workspace, error)

	// List returns all workspaces. Order is implementation-defined.
	List(ctx context.Context) ([]*Workspace, error)

	// Update applies a partial update. Returns ErrNotFound if absent.
	Update(ctx context.Context, key string, patch WorkspaceUpdate) (*Workspace, error)

	// Delete removes a workspace. Implementations should cascade-delete
	// child entities (Repos, Agents, Roles, Issues).
	// Returns ErrNotFound if absent.
	Delete(ctx context.Context, key string) error
}
