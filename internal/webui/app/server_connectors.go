package app

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/connector"
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

// buildConnectorDispatcher wires the connector egress choke point for the
// driver-op module: the workspace store's connector/grant/audit stores, the
// AES-256-GCM vault keyed by the explicit env override or the persisted local
// settings key file, and the provider registry on a timeout-bounded HTTP client.
//
// It returns nil — connector egress fails closed with a structured
// "unavailable" error — when serve has no store or no usable vault key:
// without the key sealed credentials can never be opened, so refusing every
// dispatch up front is strictly safer than failing per-call mid-flow.
func (app *Server) buildConnectorDispatcher() connectorsmodule.Dispatcher {
	if app.config.Store == nil {
		return nil
	}
	vault, err := connectorsvault.NewVaultFromEnvOrKeyFile(app.config.LocalSettingsDir)
	if err != nil {
		return nil
	}
	client := &http.Client{Timeout: connectorUpstreamTimeout()}
	registry := connector.DefaultProviderRegistry(client)
	return &connector.Dispatcher{
		Connectors: app.config.Store.Connectors(),
		Grants:     app.config.Store.ConnectorGrants(),
		Audit:      app.config.Store.ConnectorCalls(),
		Vault:      vault,
		Providers:  registry,
	}
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
