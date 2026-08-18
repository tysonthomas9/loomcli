package workflowcatalog

import (
	"context"
	"math"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	ActionResolveEffectiveVersion   authority.Action = "workflowcatalog.resolve-effective-version"
	ActionResolveRequestedVersion   authority.Action = "workflowcatalog.resolve-requested-version"
	ActionApproveVersion            authority.Action = "workflowcatalog.approve-version"
	ActionUnapproveVersion          authority.Action = "workflowcatalog.unapprove-version"
	ActionActivateVersion           authority.Action = "workflowcatalog.activate-version"
	ActionAuthorVersion             authority.Action = "workflowcatalog.author-version"
	ActionAuthorManagedVersion      authority.Action = "workflowcatalog.author-managed-version"
	ActionRecordVersionAvailability authority.Action = "workflowcatalog.record-version-availability"
	ActionApproveManagedVersion     authority.Action = "workflowcatalog.approve-managed-version"
	ActionActivateManagedVersion    authority.Action = "workflowcatalog.activate-managed-version"

	SemanticImpactVersionTrustChanged        = "workflow_catalog.version_trust_changed.v1"
	SemanticImpactEffectiveVersionChanged    = "workflow_catalog.effective_version_changed.v1"
	SemanticImpactVersionAuthored            = "workflow_catalog.version_authored.v1"
	SemanticImpactVersionAvailabilityChanged = "workflow_catalog.version_availability_changed.v1"

	// MaxExpectedRevision leaves room for FleetDB to advance a successful
	// lifecycle command by one within Redis HINCRBY and PostgreSQL BIGINT.
	MaxExpectedRevision uint64 = uint64(math.MaxInt64) - 1
)

// EffectiveVersionResolver is the narrow Workflow Catalog query intended for
// server-owned Automation callers. The absence of a requested-version input is
// deliberate: this contract can resolve only the driver's activated version.
type EffectiveVersionResolver interface {
	ResolveEffectiveVersion(ctx context.Context, auth authority.SystemAuthority, workspace, driverRef string) (*EffectiveVersion, error)
}

// RequestedVersionResolver is the explicit operator-preview query. Keeping it
// separate from EffectiveVersionResolver prevents automated dispatch from
// turning a persisted or caller-supplied version ID into the effective version.
type RequestedVersionResolver interface {
	ResolveRequestedVersion(ctx context.Context, auth authority.OperatorAuthority, workspace, driverRef, requestedVersionID string) (*RequestedVersion, error)
}

// API is the complete public Workflow Catalog surface for the Phase 2 pilot.
type API interface {
	GetDriver(ctx context.Context, workspace, driverRef string) (*Driver, error)
	ListDrivers(ctx context.Context, workspace string) ([]*Driver, error)
	GetVersion(ctx context.Context, workspace, versionID string) (*DriverVersion, error)
	ListVersions(ctx context.Context, workspace, driverRef string) (*VersionSet, error)
	EffectiveVersionResolver
	RequestedVersionResolver

	ApproveVersion(ctx context.Context, auth authority.OperatorAuthority, command VersionCommand) (*VersionResult, error)
	UnapproveVersion(ctx context.Context, auth authority.OperatorAuthority, command VersionCommand) (*VersionResult, error)
	ActivateVersion(ctx context.Context, auth authority.OperatorAuthority, command VersionCommand) (*VersionResult, error)
}

// VersionAuthoringAPI is deliberately separate from API while the paired
// FleetDB atomic authoring command is rolled out. Production composition must
// not advertise this surface until its AuthoringStore is available; callers
// may not fall back to generic Driver/DriverVersion creates or whole-record
// updates.
type VersionAuthoringAPI interface {
	// AuthorVersion admits an operator-authored immutable version. The core
	// always stamps it untrusted and never activates it; approval and
	// activation remain explicit lifecycle commands.
	AuthorVersion(context.Context, authority.OperatorAuthority, AuthorVersionCommand) (*AuthorVersionResult, error)
	// AuthorManagedVersion is the system-only lane for embedded, Loom-managed
	// builtins. It selects managed trust but still persists a pending inactive
	// version; availability and activation are later explicit transitions.
	AuthorManagedVersion(context.Context, authority.SystemAuthority, AuthorVersionCommand) (*AuthorVersionResult, error)
}

// VersionAvailabilityAPI is the system-only handoff from Workflow Authoring
// to Workflow Catalog after Workflow Distribution has promoted or rejected
// the immutable digest-addressed bundle.
type VersionAvailabilityAPI interface {
	RecordVersionAvailability(context.Context, authority.SystemAuthority, AvailabilityCommand) (*AvailabilityResult, error)
}

// ManagedVersionLifecycleAPI is the service-only completion lane for a
// Loom-managed version after distribution recorded it available. It is
// distinct from operator lifecycle authority and cannot unapprove a version.
type ManagedVersionLifecycleAPI interface {
	ApproveManagedVersion(context.Context, authority.SystemAuthority, VersionCommand) (*VersionResult, error)
	ActivateManagedVersion(context.Context, authority.SystemAuthority, VersionCommand) (*VersionResult, error)
}

// VersionSet is a driver and its versions, in the persistence-defined stable
// ordering. It gives inbound adapters the active version without another read.
type VersionSet struct {
	Driver   *Driver          `json:"driver"`
	Versions []*DriverVersion `json:"versions"`
}

// EffectiveVersion is the exact validated version activated for a driver. The
// service guarantees Driver.ActiveVersionID == Version.VersionID.
type EffectiveVersion struct {
	Driver         *Driver          `json:"driver"`
	Version        *DriverVersion   `json:"version"`
	Approved       bool             `json:"approved"`
	EffectiveTrust DriverTrustLevel `json:"effective_trust"`
}

// RequestedVersion is an exact validated version selected by explicit
// operator intent. It may be inactive, which is required for preview runs.
type RequestedVersion struct {
	Driver         *Driver          `json:"driver"`
	Version        *DriverVersion   `json:"version"`
	Active         bool             `json:"active"`
	Approved       bool             `json:"approved"`
	EffectiveTrust DriverTrustLevel `json:"effective_trust"`
}

// VersionCommand identifies one lifecycle mutation. DriverID is deliberately
// an exact durable ID rather than a display name. It cannot contain the Redis
// namespace's reserved colon delimiter. ExpectedRevision is mandatory.
// Fleet-backed driver revisions begin at one, so zero is invalid; values above
// MaxExpectedRevision cannot be advanced by FleetDB.
type VersionCommand struct {
	WorkspaceKey     string `json:"workspace_key"`
	DriverID         string `json:"driver_id"`
	VersionID        string `json:"version_id"`
	ExpectedRevision uint64 `json:"expected_revision"`
}

// VersionResult is the public result of a lifecycle command.
type VersionResult struct {
	Action            authority.Action `json:"action"`
	Driver            *Driver          `json:"driver"`
	Version           *DriverVersion   `json:"version"`
	Active            bool             `json:"active"`
	Approved          bool             `json:"approved"`
	EffectiveTrust    DriverTrustLevel `json:"effective_trust"`
	Replayed          bool             `json:"replayed,omitempty"`
	CommittedRevision uint64           `json:"committed_revision"`
	SemanticImpact    string           `json:"semantic_impact"`
}

// AuthorVersionCommand is the complete immutable catalog intent produced
// after a bundle has been built and staged locally. RequestID is the durable
// lost-response replay key. ExpectedRevision is zero only when the driver is
// expected not to exist; otherwise it is the exact aggregate CAS revision.
//
// Trust and activation are intentionally absent. The selected public method
// determines both, and the service derives CreatedBy from the admitted
// authority subject, preventing an HTTP/CLI submission from self-elevating or
// forging its audit actor.
type AuthorVersionCommand struct {
	WorkspaceKey     string            `json:"workspace_key"`
	RequestID        string            `json:"request_id"`
	ExpectedRevision uint64            `json:"expected_revision"`
	DriverID         string            `json:"driver_id"`
	DriverName       string            `json:"driver_name"`
	VersionID        string            `json:"version_id"`
	SourceRef        string            `json:"source_ref"`
	SourceDigest     string            `json:"source_digest"`
	BundleRef        string            `json:"bundle_ref"`
	BundleDigest     string            `json:"bundle_digest"`
	Runtime          string            `json:"runtime"`
	Manifest         map[string]string `json:"manifest,omitempty"`
	BuildDiagnostics string            `json:"build_diagnostics,omitempty"`
}

// AuthorVersionResult is the authoritative atomic FleetDB outcome. Driver and
// Version are post-command snapshots; CommittedRevision identifies the
// original durable commit even when a later read has advanced Driver.Revision.
type AuthorVersionResult struct {
	Action            authority.Action `json:"action"`
	Driver            *Driver          `json:"driver"`
	Version           *DriverVersion   `json:"version"`
	CreatedDriver     bool             `json:"created_driver"`
	CreatedVersion    bool             `json:"created_version"`
	ReusedVersion     bool             `json:"reused_version"`
	Replayed          bool             `json:"replayed,omitempty"`
	CommittedRevision uint64           `json:"committed_revision"`
	SemanticImpact    string           `json:"semantic_impact"`
}
