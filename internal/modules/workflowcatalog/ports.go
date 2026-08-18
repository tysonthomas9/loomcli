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

// AuthoringMutation is the transport-neutral, server-derived durable command.
// Managed is never decoded from an operator request: the Workflow Catalog
// service selects it from the typed authority lane.
// AuditActor is the admitted authority subject, never request payload data.
type AuthoringMutation struct {
	AuthorVersionCommand
	AuditActor string
	Managed    bool
}

// AuthoringResult is returned by one atomic aggregate command. Implementations
// must ensure/reuse the Driver, ensure/reuse exactly one immutable version,
// allocate a new version's positive monotonic sequence number, apply trust
// demotion, and persist availability pending without changing the active
// predecessor.
type AuthoringResult struct {
	Driver            *Driver
	Version           *DriverVersion
	CreatedDriver     bool
	CreatedVersion    bool
	ReusedVersion     bool
	Replayed          bool
	CommittedRevision uint64
	SemanticImpact    string
}

// AuthoringStore is the required FleetDB-backed command port for Phase 5
// Workflow Catalog authoring. A sequence of generic CreateDriver,
// CreateDriverVersion, trust PATCH, and activation PATCH calls is not a valid
// implementation of this port.
type AuthoringStore interface {
	AuthorVersion(context.Context, AuthoringMutation) (*AuthoringResult, error)
}

// AvailabilityMutation is the transport-neutral durable transition. The
// service derives AuditActor from the admitted SystemAuthority.
type AvailabilityMutation struct {
	AvailabilityCommand
	AuditActor string
}

// AvailabilityStore owns the one atomic availability transition.
type AvailabilityStore interface {
	RecordVersionAvailability(context.Context, AvailabilityMutation) (*AvailabilityResult, error)
}
