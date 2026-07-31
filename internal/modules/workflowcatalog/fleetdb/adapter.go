// Package fleetdb adapts the shared low-level FleetDB transport to Workflow
// Catalog-owned ports. It contains no policy; ownership, authority, and result
// validation remain in the capability core.
package fleetdb

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
)

var (
	// These sentinels are the adapter-owned vocabulary accepted from its
	// transport. The composition root translates low-level FleetDB failures to
	// this vocabulary so no infrastructure type crosses the adapter API.
	ErrTransportNotFound            = errors.New("workflow catalog fleetdb transport: not found")
	ErrTransportInvalid             = errors.New("workflow catalog fleetdb transport: invalid request")
	ErrTransportRevisionConflict    = errors.New("workflow catalog fleetdb transport: revision conflict")
	ErrTransportVersionOwnership    = errors.New("workflow catalog fleetdb transport: version ownership mismatch")
	ErrTransportVersionNotValidated = errors.New("workflow catalog fleetdb transport: version not validated")
	ErrTransportVersionNotApproved  = errors.New("workflow catalog fleetdb transport: version not approved")
	ErrTransportAuthoringConflict   = errors.New("workflow catalog fleetdb transport: authoring conflict")
)

// TransportLifecycleResult is the narrow adapter-owned representation of one
// atomic FleetDB lifecycle response.
type TransportLifecycleResult struct {
	Driver            *workflowcatalog.Driver
	Version           *workflowcatalog.DriverVersion
	Replayed          bool
	CommittedRevision uint64
	SemanticImpact    string
}

// Transport is the adapter-owned boundary implemented by the composition
// root over the process-wide FleetDB client. It deliberately uses only
// Workflow Catalog public types and adapter-owned DTOs and errors.
type Transport interface {
	GetDriver(ctx context.Context, workspace, driverID string) (*workflowcatalog.Driver, error)
	FindDriverByName(ctx context.Context, workspace, name string) (*workflowcatalog.Driver, error)
	ListDrivers(ctx context.Context, workspace string) ([]*workflowcatalog.Driver, error)
	GetVersion(ctx context.Context, workspace, versionID string) (*workflowcatalog.DriverVersion, error)
	ListVersions(ctx context.Context, workspace, driverID string) ([]*workflowcatalog.DriverVersion, error)
	ApproveVersion(ctx context.Context, workspace, driverID, versionID string, expectedRevision uint64) (*TransportLifecycleResult, error)
	UnapproveVersion(ctx context.Context, workspace, driverID, versionID string, expectedRevision uint64) (*TransportLifecycleResult, error)
	ActivateVersion(ctx context.Context, workspace, driverID, versionID string, expectedRevision uint64) (*TransportLifecycleResult, error)
}

// TransportAuthoringResult is the narrow response from FleetDB's atomic
// authoring command.
type TransportAuthoringResult struct {
	Driver            *workflowcatalog.Driver
	Version           *workflowcatalog.DriverVersion
	CreatedDriver     bool
	CreatedVersion    bool
	ReusedVersion     bool
	Activated         bool
	Replayed          bool
	CommittedRevision uint64
	SemanticImpact    string
}

// AuthoringTransport is separate from Transport so lifecycle-only FleetDB
// deployments remain constructible while the paired Phase 5 route rolls out.
// Production must use NewWithAuthoring before exposing VersionAuthoringAPI.
type AuthoringTransport interface {
	AuthorVersion(context.Context, workflowcatalog.AuthoringMutation) (*TransportAuthoringResult, error)
}

// Adapter implements both catalog-owned persistence ports over one injected
// transport. The transport is obtained from the composition root's existing
// FleetDB Client and must not be constructed here.
type Adapter struct {
	transport Transport
	authoring AuthoringTransport
}

var (
	_ workflowcatalog.Reader                = (*Adapter)(nil)
	_ workflowcatalog.VersionLifecycleStore = (*Adapter)(nil)
	_ workflowcatalog.AuthoringStore        = (*Adapter)(nil)
)

// New accepts the narrow adapter-owned transport supplied by the composition
// root. The adapter never constructs or exposes a low-level FleetDB client.
func New(transport Transport) (*Adapter, error) {
	if transport == nil {
		return nil, fmt.Errorf("workflow catalog fleetdb adapter: nil transport: %w", workflowcatalog.ErrUnavailable)
	}
	return &Adapter{transport: transport}, nil
}

// NewWithAuthoring composes the complete Phase 5 persistence adapter.
func NewWithAuthoring(transport Transport, authoring AuthoringTransport) (*Adapter, error) {
	adapter, err := New(transport)
	if err != nil {
		return nil, err
	}
	if authoring == nil {
		return nil, fmt.Errorf("workflow catalog fleetdb adapter: nil authoring transport: %w", workflowcatalog.ErrUnavailable)
	}
	adapter.authoring = authoring
	return adapter, nil
}

func (a *Adapter) GetDriver(ctx context.Context, workspace, driverID string) (*workflowcatalog.Driver, error) {
	value, err := a.transport.GetDriver(ctx, workspace, driverID)
	return value, mapError("get driver", err)
}

func (a *Adapter) FindDriverByName(ctx context.Context, workspace, name string) (*workflowcatalog.Driver, error) {
	value, err := a.transport.FindDriverByName(ctx, workspace, name)
	return value, mapError("find driver", err)
}

func (a *Adapter) ListDrivers(ctx context.Context, workspace string) ([]*workflowcatalog.Driver, error) {
	values, err := a.transport.ListDrivers(ctx, workspace)
	return values, mapError("list drivers", err)
}

func (a *Adapter) GetVersion(ctx context.Context, workspace, versionID string) (*workflowcatalog.DriverVersion, error) {
	value, err := a.transport.GetVersion(ctx, workspace, versionID)
	return value, mapError("get version", err)
}

func (a *Adapter) ListVersions(ctx context.Context, workspace, driverID string) ([]*workflowcatalog.DriverVersion, error) {
	values, err := a.transport.ListVersions(ctx, workspace, driverID)
	return values, mapError("list versions", err)
}

func (a *Adapter) ApproveVersion(ctx context.Context, mutation workflowcatalog.LifecycleMutation) (*workflowcatalog.LifecycleResult, error) {
	result, err := a.transport.ApproveVersion(ctx, mutation.WorkspaceKey, mutation.DriverID, mutation.VersionID, mutation.ExpectedRevision)
	return lifecycleResult("approve version", result, err)
}

func (a *Adapter) UnapproveVersion(ctx context.Context, mutation workflowcatalog.LifecycleMutation) (*workflowcatalog.LifecycleResult, error) {
	result, err := a.transport.UnapproveVersion(ctx, mutation.WorkspaceKey, mutation.DriverID, mutation.VersionID, mutation.ExpectedRevision)
	return lifecycleResult("unapprove version", result, err)
}

func (a *Adapter) ActivateVersion(ctx context.Context, mutation workflowcatalog.LifecycleMutation) (*workflowcatalog.LifecycleResult, error) {
	result, err := a.transport.ActivateVersion(ctx, mutation.WorkspaceKey, mutation.DriverID, mutation.VersionID, mutation.ExpectedRevision)
	return lifecycleResult("activate version", result, err)
}

func (a *Adapter) AuthorVersion(ctx context.Context, mutation workflowcatalog.AuthoringMutation) (*workflowcatalog.AuthoringResult, error) {
	if a == nil || a.authoring == nil {
		return nil, workflowcatalog.ErrUnavailable
	}
	result, err := a.authoring.AuthorVersion(ctx, mutation)
	if err != nil {
		return nil, mapError("author version", err)
	}
	if result == nil {
		return nil, fmt.Errorf("author version: empty FleetDB response: %w", workflowcatalog.ErrInvalidPersistedState)
	}
	return &workflowcatalog.AuthoringResult{
		Driver: result.Driver, Version: result.Version,
		CreatedDriver: result.CreatedDriver, CreatedVersion: result.CreatedVersion,
		ReusedVersion: result.ReusedVersion, Activated: result.Activated,
		Replayed: result.Replayed, CommittedRevision: result.CommittedRevision,
		SemanticImpact: result.SemanticImpact,
	}, nil
}

func lifecycleResult(operation string, result *TransportLifecycleResult, err error) (*workflowcatalog.LifecycleResult, error) {
	if err != nil {
		return nil, mapError(operation, err)
	}
	if result == nil {
		return nil, fmt.Errorf("%s: empty FleetDB response: %w", operation, workflowcatalog.ErrInvalidPersistedState)
	}
	return &workflowcatalog.LifecycleResult{
		Driver:            result.Driver,
		Version:           result.Version,
		Replayed:          result.Replayed,
		CommittedRevision: result.CommittedRevision,
		SemanticImpact:    result.SemanticImpact,
	}, nil
}

func mapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var mapped error
	switch {
	case errors.Is(err, ErrTransportNotFound):
		mapped = workflowcatalog.ErrNotFound
	case errors.Is(err, ErrTransportRevisionConflict):
		mapped = workflowcatalog.ErrStaleRevision
	case errors.Is(err, ErrTransportVersionOwnership):
		mapped = workflowcatalog.ErrVersionOwnership
	case errors.Is(err, ErrTransportVersionNotValidated):
		mapped = workflowcatalog.ErrVersionNotValidated
	case errors.Is(err, ErrTransportVersionNotApproved):
		mapped = workflowcatalog.ErrVersionNotApproved
	case errors.Is(err, ErrTransportAuthoringConflict):
		mapped = workflowcatalog.ErrAuthoringConflict
	case errors.Is(err, ErrTransportInvalid):
		mapped = workflowcatalog.ErrInvalid
	default:
		mapped = workflowcatalog.ErrUnavailable
	}
	return fmt.Errorf("%s: %w", operation, errors.Join(mapped, err))
}
