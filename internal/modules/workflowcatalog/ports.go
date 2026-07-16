package workflowcatalog

import "context"

// Reader is the catalog-owned read port. Implementations scope every method to
// one workspace; the core defensively verifies returned ownership as well.
type Reader interface {
	GetDriver(ctx context.Context, workspace, driverID string) (*Driver, error)
	FindDriverByName(ctx context.Context, workspace, name string) (*Driver, error)
	ListDrivers(ctx context.Context, workspace string) ([]*Driver, error)
	GetVersion(ctx context.Context, workspace, versionID string) (*DriverVersion, error)
	ListVersions(ctx context.Context, workspace, driverID string) ([]*DriverVersion, error)
}

// LifecycleMutation is the transport-neutral command sent to durable
// persistence. FleetDB derives replay identity from these aggregate and action
// coordinates at its trust boundary; callers cannot supply actor or
// idempotency fields through this port.
type LifecycleMutation struct {
	WorkspaceKey     string
	DriverID         string
	VersionID        string
	ExpectedRevision uint64
}

// LifecycleResult is the authoritative result returned by durable storage.
// CommittedRevision identifies this command's original commit. Driver is a
// post-commit read and may reflect later durable transitions, whether or not
// this particular response was marked as a replay.
type LifecycleResult struct {
	Driver            *Driver
	Version           *DriverVersion
	Replayed          bool
	CommittedRevision uint64
	SemanticImpact    string
}

// VersionLifecycleStore is the catalog-owned durable command port. Each method
// must atomically revalidate ownership, validation/approval preconditions, and
// expected revision before committing.
type VersionLifecycleStore interface {
	ApproveVersion(ctx context.Context, mutation LifecycleMutation) (*LifecycleResult, error)
	UnapproveVersion(ctx context.Context, mutation LifecycleMutation) (*LifecycleResult, error)
	ActivateVersion(ctx context.Context, mutation LifecycleMutation) (*LifecycleResult, error)
}
