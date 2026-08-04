package sourcecontrolcomposition

import (
	"context"
	"errors"
	"time"

	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	connectorsfleetdb "github.com/tysonthomas9/loomcli/internal/modules/connectors/fleetdb"
)

type connectorsFleetDBTransport struct {
	grants infrafleetdb.ConnectorGrantTransport
}

var _ connectorsfleetdb.Transport = (*connectorsFleetDBTransport)(nil)

func newConnectorsFleetDBTransport(client *infrafleetdb.Client) connectorsfleetdb.Transport {
	if client == nil {
		return nil
	}
	return &connectorsFleetDBTransport{grants: client.ConnectorGrantCommands()}
}

func (transport *connectorsFleetDBTransport) CreateConnectorGrant(
	ctx context.Context,
	input connectorsfleetdb.CreateConnectorGrantWire,
) (*connectorsfleetdb.ConnectorGrantWire, error) {
	value, err := transport.grants.CreateConnectorGrant(ctx, infrafleetdb.ConnectorGrantCreateCommand{
		WorkspaceKey: input.WorkspaceKey, GrantID: input.GrantID,
		ConnectorID: input.ConnectorID, BindingID: input.BindingID,
		Action: input.Action, ResourcePattern: input.ResourcePattern,
	})
	return connectorGrantWire(value), translateConnectorsFleetDBError(err)
}

func (transport *connectorsFleetDBTransport) ListConnectorGrants(
	ctx context.Context,
	workspace string,
	filter connectorsfleetdb.ConnectorGrantFilterWire,
) ([]*connectorsfleetdb.ConnectorGrantWire, error) {
	values, err := transport.grants.ListConnectorGrantsByBinding(ctx, workspace, filter.BindingID)
	if err != nil {
		return nil, translateConnectorsFleetDBError(err)
	}
	out := make([]*connectorsfleetdb.ConnectorGrantWire, len(values))
	for index, value := range values {
		out[index] = connectorGrantWire(value)
	}
	return out, nil
}

func connectorGrantWire(value *infrafleetdb.ConnectorGrantRecord) *connectorsfleetdb.ConnectorGrantWire {
	if value == nil {
		return nil
	}
	return &connectorsfleetdb.ConnectorGrantWire{
		WorkspaceKey: value.WorkspaceKey, GrantID: value.GrantID,
		ConnectorID: value.ConnectorID, BindingID: value.BindingID,
		Action: value.Action, ResourcePattern: value.ResourcePattern,
		CreatedAt: value.CreatedAt, RevokedAt: cloneConnectorTime(value.RevokedAt),
	}
}

func cloneConnectorTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func translateConnectorsFleetDBError(err error) error {
	if err == nil {
		return nil
	}
	var translated error
	switch {
	case errors.Is(err, infrafleetdb.ErrConnectorGrantNotFound):
		translated = connectorsfleetdb.ErrTransportNotFound
	case errors.Is(err, infrafleetdb.ErrConnectorGrantInvalid):
		translated = connectorsfleetdb.ErrTransportInvalid
	case errors.Is(err, infrafleetdb.ErrConnectorGrantConflict):
		translated = connectorsfleetdb.ErrTransportConflict
	default:
		translated = connectorsfleetdb.ErrTransportUnavailable
	}
	return errors.Join(translated, err)
}
