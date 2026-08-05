package workflowcatalog

import (
	"context"
	"math"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	ActionResolveEffectiveVersion authority.Action = "workflowcatalog.resolve-effective-version"
	ActionResolveRequestedVersion authority.Action = "workflowcatalog.resolve-requested-version"
	ActionApproveVersion          authority.Action = "workflowcatalog.approve-version"
	ActionUnapproveVersion        authority.Action = "workflowcatalog.unapprove-version"
	ActionActivateVersion         authority.Action = "workflowcatalog.activate-version"

	SemanticImpactVersionTrustChanged     = "workflow_catalog.version_trust_changed.v1"
	SemanticImpactEffectiveVersionChanged = "workflow_catalog.effective_version_changed.v1"

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
