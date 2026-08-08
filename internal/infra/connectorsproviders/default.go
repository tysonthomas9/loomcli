// Default provider composition stays beside the concrete provider registry.
package connectorsproviders

import (
	"net/http"
	"os"
	"strings"

	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

const (
	GitHubBaseURLEnvVar  = "LOOM_CONNECTOR_GITHUB_BASE_URL"
	SlackBaseURLEnvVar   = "LOOM_CONNECTOR_SLACK_BASE_URL"
	DatadogBaseURLEnvVar = "LOOM_CONNECTOR_DATADOG_BASE_URL"
)

func Default(client *http.Client) *Registry {
	registry := NewRegistry()
	_ = registry.Register(
		connectorsmodule.ConnectorSourceGitHub,
		NewGitHub(client, baseURLOverride(GitHubBaseURLEnvVar)),
	)
	_ = registry.Register(
		connectorsmodule.ConnectorSourceSlack,
		NewSlack(client, baseURLOverride(SlackBaseURLEnvVar)),
	)
	_ = registry.Register(
		connectorsmodule.ConnectorSourceDatadog,
		NewDatadog(client, baseURLOverride(DatadogBaseURLEnvVar)),
	)
	return registry
}

func baseURLOverride(environmentVariable string) string {
	return strings.TrimSpace(os.Getenv(environmentVariable))
}
