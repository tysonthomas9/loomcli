package app

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/infra/connectorsproviders"
	"github.com/tysonthomas9/loomcli/internal/infra/connectorsvault"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

// Connector egress wiring for the driver-op HTTP API (CV9). Kept out of
// server_modules.go so the contested module-wiring file carries only the one
// Dispatcher line.
const (
	// ConnectorUpstreamTimeoutEnvVar overrides the per-call timeout on the
	// HTTP client connector providers egress through (a Go duration string,
	// e.g. "45s").
	ConnectorUpstreamTimeoutEnvVar = "LOOM_CONNECTOR_UPSTREAM_TIMEOUT"
	// defaultConnectorUpstreamTimeout bounds provider calls when the env
	// knob is unset or unparseable.
	defaultConnectorUpstreamTimeout = 30 * time.Second
)

// buildConnectorCapabilities wires the connector egress and management owner
// interfaces from one durable adapter and one vault. The route modules receive
// only those owner interfaces; the composite persistence store remains in the
// application composition root.
//
// It returns nil interfaces — connector operations fail closed with a
// structured "unavailable" error — when serve has no store or usable vault key:
// without the key sealed credentials can never be opened, so refusing every
// dispatch up front is strictly safer than failing per-call mid-flow.
func (app *Server) buildConnectorCapabilities() (
	connectorsmodule.Dispatcher,
	connectorsmodule.Management,
	connectorsmodule.CredentialSealer,
) {
	if app.config.ProjectionRecords == nil {
		return nil, nil, nil
	}
	vault, err := connectorsvault.NewVaultFromEnvOrKeyFile(app.config.LocalSettingsDir)
	if err != nil {
		return nil, nil, nil
	}
	store := app.config.ProjectionRecords.Connectors()
	dispatcher, err := connectorsmodule.NewDispatch(
		store,
		vault,
		connectorsproviders.Default(&http.Client{Timeout: connectorUpstreamTimeout()}),
		nil,
	)
	if err != nil {
		return nil, nil, nil
	}
	vaultAdapter, err := connectorsvault.New(vault)
	if err != nil {
		return nil, nil, nil
	}
	management, err := connectorsmodule.NewManagementWithCredentialVault(store, vaultAdapter, time.Now)
	if err != nil {
		return nil, nil, nil
	}
	return dispatcher, management, vault
}

// connectorUpstreamTimeout resolves the provider HTTP client timeout from
// LOOM_CONNECTOR_UPSTREAM_TIMEOUT, defaulting to 30s on empty, unparseable
// or non-positive values.
func connectorUpstreamTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv(ConnectorUpstreamTimeoutEnvVar))
	if raw == "" {
		return defaultConnectorUpstreamTimeout
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return defaultConnectorUpstreamTimeout
	}
	return parsed
}
