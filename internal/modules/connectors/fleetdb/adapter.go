// Package fleetdb adapts the shared low-level FleetDB transport to
// Connectors-owned grant ports. It owns wire mapping and transport error
// translation only; authority, immutable-collision policy, race recovery, and
// response validation remain in the Connectors capability core.
package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

var (
	ErrTransportNotFound      = errors.New("connectors fleetdb transport: not found")
	ErrTransportInvalid       = errors.New("connectors fleetdb transport: invalid request")
	ErrTransportAlreadyExists = errors.New("connectors fleetdb transport: already exists")
	ErrTransportConflict      = errors.New("connectors fleetdb transport: conflict")
	ErrTransportUnavailable   = errors.New("connectors fleetdb transport: unavailable")
)

// ConnectorGrantWire mirrors FleetDB's ConnectorGrant response without
// importing the legacy domain or store packages.
type ConnectorGrantWire struct {
	WorkspaceKey    string     `json:"workspace_key"`
	GrantID         string     `json:"grant_id"`
	ConnectorID     string     `json:"connector_id"`
	BindingID       string     `json:"binding_id"`
	Action          string     `json:"action"`
	ResourcePattern string     `json:"resource_pattern"`
	CreatedAt       time.Time  `json:"created_at"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
}

type CreateConnectorGrantWire struct {
	WorkspaceKey    string
	GrantID         string
	ConnectorID     string
	BindingID       string
	Action          string
	ResourcePattern string
}

type ConnectorGrantFilterWire struct {
	BindingID string
}

// Transport is the exact FleetDB surface needed by Connector grant
// provisioning. FleetDB currently exposes create and filtered list routes,
// not Get-by-grant-ID; composition must not emulate or invent another route.
type Transport interface {
	CreateConnectorGrant(context.Context, CreateConnectorGrantWire) (*ConnectorGrantWire, error)
	ListConnectorGrants(context.Context, string, ConnectorGrantFilterWire) ([]*ConnectorGrantWire, error)
}

type Adapter struct {
	transport Transport
}

var _ connectors.ConnectorGrantStore = (*Adapter)(nil)

func New(transport Transport) (*Adapter, error) {
	if transport == nil {
		return nil, fmt.Errorf("connectors fleetdb adapter: nil transport: %w", connectors.ErrUnavailable)
	}
	return &Adapter{transport: transport}, nil
}

func (adapter *Adapter) CreateGrant(
	ctx context.Context,
	mutation connectors.CreateGrantMutation,
) (*connectors.ConnectorGrant, error) {
	value, err := adapter.transport.CreateConnectorGrant(ctx, CreateConnectorGrantWire{
		WorkspaceKey: mutation.WorkspaceKey, GrantID: mutation.GrantID,
		ConnectorID: mutation.ConnectorID, BindingID: mutation.BindingID,
		Action: mutation.Action, ResourcePattern: mutation.ResourcePattern,
	})
	if err != nil {
		return nil, mapError("create connector grant", err)
	}
	return grantFromWire(value), nil
}

func (adapter *Adapter) ListGrantsByBinding(
	ctx context.Context,
	workspace,
	bindingID string,
) ([]*connectors.ConnectorGrant, error) {
	values, err := adapter.transport.ListConnectorGrants(
		ctx,
		workspace,
		ConnectorGrantFilterWire{BindingID: bindingID},
	)
	if err != nil {
		return nil, mapError("list connector grants", err)
	}
	out := make([]*connectors.ConnectorGrant, len(values))
	for index, value := range values {
		out[index] = grantFromWire(value)
	}
	return out, nil
}

func grantFromWire(value *ConnectorGrantWire) *connectors.ConnectorGrant {
	if value == nil {
		return nil
	}
	out := &connectors.ConnectorGrant{
		WorkspaceKey: value.WorkspaceKey, GrantID: value.GrantID,
		ConnectorID: value.ConnectorID, BindingID: value.BindingID,
		Action: value.Action, ResourcePattern: value.ResourcePattern,
		CreatedAt: value.CreatedAt,
	}
	if value.RevokedAt != nil {
		revokedAt := *value.RevokedAt
		out.RevokedAt = &revokedAt
	}
	return out
}

func mapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var mapped error
	switch {
	case errors.Is(err, ErrTransportNotFound):
		mapped = connectors.ErrNotFound
	case errors.Is(err, ErrTransportInvalid):
		mapped = connectors.ErrInvalid
	case errors.Is(err, ErrTransportAlreadyExists), errors.Is(err, ErrTransportConflict):
		mapped = connectors.ErrGrantConflict
	default:
		mapped = connectors.ErrUnavailable
	}
	return fmt.Errorf("%s: %w", operation, errors.Join(mapped, err))
}
