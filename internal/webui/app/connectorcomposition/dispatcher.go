// Package connectorcomposition assembles the WebUI connector egress choke
// point from capability-owned persistence, credential, and provider adapters.
package connectorcomposition

import (
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/infra/connectorscatalog"
	"github.com/tysonthomas9/loomcli/internal/infra/connectorsproviders"
	"github.com/tysonthomas9/loomcli/internal/infra/connectorsvault"
	"github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type connectorPersistence interface {
	Connectors() store.ConnectorStore
	ConnectorGrants() store.ConnectorGrantStore
	ConnectorCalls() store.ConnectorAuditStore
}

// BuildDispatcher wires the connector egress choke point from the shared
// process store and its persisted credential-vault source. It returns nil when
// those dependencies cannot provide a complete, fail-closed dispatcher.
func BuildDispatcher(state connectorPersistence, localSettingsDir string, timeout time.Duration) connectors.Dispatcher {
	if state == nil {
		return nil
	}
	vault, err := connectorsvault.NewVaultFromEnvOrKeyFile(localSettingsDir)
	if err != nil {
		return nil
	}
	catalog, err := connectorscatalog.New(
		state.Connectors(),
		state.ConnectorGrants(),
		state.ConnectorCalls(),
	)
	if err != nil {
		return nil
	}
	dispatcher, err := connectors.NewDispatch(
		catalog,
		vault,
		connectorsproviders.Default(&http.Client{Timeout: timeout}),
		nil,
	)
	if err != nil {
		return nil
	}
	return dispatcher
}
