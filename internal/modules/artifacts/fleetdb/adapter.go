// Package fleetdb adapts the process-wide FleetDB transport to the
// Artifacts-owned durable port. It contains no content, ownership, or retry
// policy and never constructs a FleetDB client.
package fleetdb

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
)

var (
	ErrTransportNotFound          = errors.New("artifacts fleetdb transport: not found")
	ErrTransportInvalid           = errors.New("artifacts fleetdb transport: invalid request")
	ErrTransportConflict          = errors.New("artifacts fleetdb transport: conflict")
	ErrTransportNotOwner          = errors.New("artifacts fleetdb transport: not owner")
	ErrTransportInvalidTransition = errors.New("artifacts fleetdb transport: invalid transition")
	ErrTransportUnavailable       = errors.New("artifacts fleetdb transport: unavailable")
)

// Transport is the adapter-owned boundary implemented by composition over
// the shared low-level FleetDB client. Each method is one owner-fenced FleetDB
// command or query; implementations must not split authorization from the
// durable transition.
type Transport interface {
	Create(context.Context, artifacts.ExecutionOwner, artifacts.CreateCommand) (*artifacts.Artifact, error)
	Upload(context.Context, artifacts.ExecutionOwner, artifacts.UploadCommand) (*artifacts.Artifact, error)
	Finalize(context.Context, artifacts.ExecutionOwner, artifacts.FinalizeCommand) (*artifacts.Artifact, error)
	Reference(context.Context, artifacts.ExecutionOwner, artifacts.ReferenceCommand) (artifacts.ReferenceResult, error)
	Get(context.Context, artifacts.ExecutionOwner, artifacts.GetQuery) (*artifacts.Artifact, error)
	List(context.Context, artifacts.ExecutionOwner, artifacts.ListFilter) ([]*artifacts.Artifact, error)
}

// Adapter implements the Artifacts-owned persistence port. The transport is
// supplied by the composition root and reuses the one process-wide FleetDB
// client's authentication, tracing, and connection pool.
type Adapter struct {
	transport Transport
}

var _ artifacts.Store = (*Adapter)(nil)

func New(transport Transport) (*Adapter, error) {
	if transport == nil {
		return nil, fmt.Errorf("artifacts fleetdb adapter: nil transport: %w", artifacts.ErrUnavailable)
	}
	return &Adapter{transport: transport}, nil
}

func (adapter *Adapter) Create(ctx context.Context, owner artifacts.ExecutionOwner, command artifacts.CreateCommand) (*artifacts.Artifact, error) {
	value, err := adapter.transport.Create(ctx, owner, command)
	return value, mapError("create artifact", err)
}

func (adapter *Adapter) Upload(ctx context.Context, owner artifacts.ExecutionOwner, command artifacts.UploadCommand) (*artifacts.Artifact, error) {
	value, err := adapter.transport.Upload(ctx, owner, command)
	return value, mapError("upload artifact", err)
}

func (adapter *Adapter) Finalize(ctx context.Context, owner artifacts.ExecutionOwner, command artifacts.FinalizeCommand) (*artifacts.Artifact, error) {
	value, err := adapter.transport.Finalize(ctx, owner, command)
	return value, mapError("finalize artifact", err)
}

func (adapter *Adapter) Reference(ctx context.Context, owner artifacts.ExecutionOwner, command artifacts.ReferenceCommand) (artifacts.ReferenceResult, error) {
	value, err := adapter.transport.Reference(ctx, owner, command)
	return value, mapError("reference artifact", err)
}

func (adapter *Adapter) Get(ctx context.Context, owner artifacts.ExecutionOwner, query artifacts.GetQuery) (*artifacts.Artifact, error) {
	value, err := adapter.transport.Get(ctx, owner, query)
	return value, mapError("get artifact", err)
}

func (adapter *Adapter) List(ctx context.Context, owner artifacts.ExecutionOwner, filter artifacts.ListFilter) ([]*artifacts.Artifact, error) {
	values, err := adapter.transport.List(ctx, owner, filter)
	return values, mapError("list artifacts", err)
}

func mapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var mapped error
	switch {
	case errors.Is(err, ErrTransportNotFound):
		mapped = artifacts.ErrNotFound
	case errors.Is(err, ErrTransportNotOwner):
		mapped = artifacts.ErrNotOwner
	case errors.Is(err, ErrTransportInvalidTransition):
		mapped = artifacts.ErrInvalidTransition
	case errors.Is(err, ErrTransportConflict):
		mapped = artifacts.ErrAlreadyExists
	case errors.Is(err, ErrTransportInvalid):
		mapped = artifacts.ErrInvalid
	case errors.Is(err, ErrTransportUnavailable):
		mapped = artifacts.ErrUnavailable
	default:
		mapped = artifacts.ErrUnavailable
	}
	return fmt.Errorf("%s: %w", operation, errors.Join(mapped, err))
}
