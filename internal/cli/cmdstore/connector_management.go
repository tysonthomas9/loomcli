package cmdstore

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/connectorscatalog"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

// ConnectorManagement composes the Connectors owner API over the shared Store
// handle. CLI adapters do not receive Connector, Grant, or audit sub-stores.
func ConnectorManagement(handle *bootstrap.StoreHandle) (connectorsmodule.Management, error) {
	if handle == nil || handle.Store == nil {
		return nil, fmt.Errorf("compose Connectors capability: %w", connectorsmodule.ErrUnavailable)
	}
	adapter, err := connectorscatalog.New(
		handle.Store.Connectors(),
		handle.Store.ConnectorGrants(),
		handle.Store.ConnectorCalls(),
	)
	if err != nil {
		return nil, fmt.Errorf("compose Connectors capability: %w", err)
	}
	management, err := connectorsmodule.NewManagement(adapter)
	if err != nil {
		return nil, fmt.Errorf("compose Connectors capability: %w", err)
	}
	return management, nil
}

func WithActiveConnectorManagement(
	fn func(context.Context, *bootstrap.StoreHandle, connectorsmodule.Management, string) error,
) error {
	return WithActiveWorkspace(func(ctx context.Context, handle *bootstrap.StoreHandle, workspace string) error {
		management, err := ConnectorManagement(handle)
		if err != nil {
			return err
		}
		return fn(ctx, handle, management, workspace)
	})
}
