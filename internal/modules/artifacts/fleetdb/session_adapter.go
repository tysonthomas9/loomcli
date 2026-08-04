package fleetdb

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
)

// SessionTransport is the adapter-owned boundary implemented by composition
// over the process-wide FleetDB client's session Artifact surface.
type SessionTransport interface {
	CreateSession(context.Context, artifacts.SessionOwner, artifacts.CreateCommand) (*artifacts.Artifact, error)
	UploadSession(context.Context, artifacts.SessionOwner, artifacts.UploadCommand) (*artifacts.Artifact, error)
	FinalizeSession(context.Context, artifacts.SessionOwner, artifacts.FinalizeCommand) (*artifacts.Artifact, error)
	GetSession(context.Context, artifacts.SessionOwner, artifacts.GetQuery) (*artifacts.Artifact, error)
}

type SessionAdapter struct {
	transport SessionTransport
}

var _ artifacts.SessionStore = (*SessionAdapter)(nil)

func NewSession(transport SessionTransport) (*SessionAdapter, error) {
	if transport == nil {
		return nil, fmt.Errorf("artifacts FleetDB session adapter: nil transport: %w", artifacts.ErrUnavailable)
	}
	return &SessionAdapter{transport: transport}, nil
}

func (adapter *SessionAdapter) CreateSession(ctx context.Context, owner artifacts.SessionOwner, command artifacts.CreateCommand) (*artifacts.Artifact, error) {
	value, err := adapter.transport.CreateSession(ctx, owner, command)
	return value, mapError("create session artifact", err)
}

func (adapter *SessionAdapter) UploadSession(ctx context.Context, owner artifacts.SessionOwner, command artifacts.UploadCommand) (*artifacts.Artifact, error) {
	value, err := adapter.transport.UploadSession(ctx, owner, command)
	return value, mapError("upload session artifact", err)
}

func (adapter *SessionAdapter) FinalizeSession(ctx context.Context, owner artifacts.SessionOwner, command artifacts.FinalizeCommand) (*artifacts.Artifact, error) {
	value, err := adapter.transport.FinalizeSession(ctx, owner, command)
	return value, mapError("finalize session artifact", err)
}

func (adapter *SessionAdapter) GetSession(ctx context.Context, owner artifacts.SessionOwner, query artifacts.GetQuery) (*artifacts.Artifact, error) {
	value, err := adapter.transport.GetSession(ctx, owner, query)
	return value, mapError("get session artifact", err)
}
