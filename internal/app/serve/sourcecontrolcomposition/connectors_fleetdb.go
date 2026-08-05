package sourcecontrolcomposition

import (
	"context"
	"errors"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	connectorsfleetdb "github.com/tysonthomas9/loomcli/internal/modules/connectors/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type connectorsFleetDBTransport struct {
	grants store.ConnectorGrantStore
}

var _ connectorsfleetdb.Transport = (*connectorsFleetDBTransport)(nil)

func newConnectorsFleetDBTransport(client *infrafleetdb.Client) connectorsfleetdb.Transport {
	if client == nil {
		return nil
	}
	return &connectorsFleetDBTransport{grants: client.ConnectorGrants()}
}

func (transport *connectorsFleetDBTransport) CreateConnectorGrant(
	ctx context.Context,
	input connectorsfleetdb.CreateConnectorGrantWire,
) (*connectorsfleetdb.ConnectorGrantWire, error) {
	value, err := transport.grants.Create(ctx, store.ConnectorGrantCreate{
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
	values, err := transport.grants.ListByBinding(ctx, workspace, filter.BindingID)
	if err != nil {
		return nil, translateConnectorsFleetDBError(err)
	}
	out := make([]*connectorsfleetdb.ConnectorGrantWire, len(values))
	for index, value := range values {
		out[index] = connectorGrantWire(value)
	}
	return out, nil
}

func connectorGrantWire(value *domain.ConnectorGrant) *connectorsfleetdb.ConnectorGrantWire {
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
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrConnectorNotFound):
		translated = connectorsfleetdb.ErrTransportNotFound
	case errors.Is(err, domain.ErrInvalid):
		translated = connectorsfleetdb.ErrTransportInvalid
	case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrConnectorExists):
		translated = connectorsfleetdb.ErrTransportAlreadyExists
	case errors.Is(err, domain.ErrConflict):
		translated = connectorsfleetdb.ErrTransportConflict
	default:
		translated = connectorsfleetdb.ErrTransportUnavailable
	}
	return errors.Join(translated, err)
}
