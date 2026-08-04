package artifacts

import "context"

// Store is the Artifacts-owned durable command/query port. Every operation
// receives the execution owner tuple so a Fleet-backed implementation can
// validate resource, holder, token, and fence in the same owner command as the
// artifact transition. It intentionally does not expose generic CRUD/update.
type Store interface {
	Create(context.Context, ExecutionOwner, CreateCommand) (*Artifact, error)
	Upload(context.Context, ExecutionOwner, UploadCommand) (*Artifact, error)
	Finalize(context.Context, ExecutionOwner, FinalizeCommand) (*Artifact, error)
	Reference(context.Context, ExecutionOwner, ReferenceCommand) (ReferenceResult, error)
	Get(context.Context, ExecutionOwner, GetQuery) (*Artifact, error)
	List(context.Context, ExecutionOwner, ListFilter) ([]*Artifact, error)
}

// SessionStore is the Artifacts-owned durable port for session content. Its
// transport persists only the derived session owner; the module separately
// verifies the issuer-bound live generation carried by SessionAuthority.
// Interaction consumes the one-use lease credential only when it atomically
// attaches the finalized Artifact to the AgentSession.
type SessionStore interface {
	CreateSession(context.Context, SessionOwner, CreateCommand) (*Artifact, error)
	UploadSession(context.Context, SessionOwner, UploadCommand) (*Artifact, error)
	FinalizeSession(context.Context, SessionOwner, FinalizeCommand) (*Artifact, error)
	GetSession(context.Context, SessionOwner, GetQuery) (*Artifact, error)
}
