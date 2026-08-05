package connectors

import "context"

// ConnectorGrantStore is the Connectors-owned persistence port for the two
// Fleet operations required by EnsureGrant. FleetDB has no Get-by-grant-ID
// route, so implementations must use the binding-filtered active listing and
// must not invent a hidden read path.
type ConnectorGrantStore interface {
	CreateGrant(context.Context, CreateGrantMutation) (*ConnectorGrant, error)
	ListGrantsByBinding(context.Context, string, string) ([]*ConnectorGrant, error)
}

type CreateGrantMutation struct {
	WorkspaceKey    string
	GrantID         string
	ConnectorID     string
	BindingID       string
	Action          string
	ResourcePattern string
}

// GitReadExecutor is the Connectors-owned provider-dispatch port. Concrete
// implementations may resolve a secret internally, but neither this port nor
// its command/result types contain plaintext credential fields.
type GitReadExecutor interface {
	// ValidateGitRead resolves and validates the real filesystem containment
	// immediately before execution. It must reject symlinked workspace, parent,
	// and target paths and return a stable resolved target identity for locking.
	ValidateGitRead(context.Context, GitReadCommand) (string, error)
	ExecuteGitRead(context.Context, GitReadCommand) error
}
