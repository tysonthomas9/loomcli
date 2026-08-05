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
