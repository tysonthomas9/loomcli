package connector

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// DefaultProviderRegistry builds the standard provider registry — GitHub,
// Slack, and Datadog adapters against their production base URLs — on the
// given HTTP client. Serve's connector wiring uses this so callers outside
// this package never assemble provider sets themselves.
func DefaultProviderRegistry(client *http.Client) *providers.Registry {
	registry := providers.NewRegistry()
	// Register errors only on nil providers or duplicate kinds; neither can
	// happen on this freshly built registry.
	_ = registry.Register(domain.ConnectorSourceGitHub, providers.NewGitHub(client, ""))
	_ = registry.Register(domain.ConnectorSourceSlack, providers.NewSlack(client, ""))
	_ = registry.Register(domain.ConnectorSourceDatadog, providers.NewDatadog(client, ""))
	return registry
}
