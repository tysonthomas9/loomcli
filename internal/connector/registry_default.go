package connector

import (
	"net/http"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// Provider base-URL override env vars. These are deployment/test seams so a
// hermetic stack (e.g. deploy/podman-stack) can point connector egress at a
// stub upstream, or an enterprise deployment at a GHES/proxy endpoint.
// Unset/empty values fall back to each provider's production base URL.
const (
	// GitHubBaseURLEnvVar overrides the GitHub REST API base URL.
	GitHubBaseURLEnvVar = "LOOM_CONNECTOR_GITHUB_BASE_URL"
	// SlackBaseURLEnvVar overrides the Slack Web API base URL.
	SlackBaseURLEnvVar = "LOOM_CONNECTOR_SLACK_BASE_URL"
	// DatadogBaseURLEnvVar overrides the Datadog API base URL.
	DatadogBaseURLEnvVar = "LOOM_CONNECTOR_DATADOG_BASE_URL"

	// FakeProviderEnvVar installs the in-process recording provider instead
	// of real upstream adapters when set to 1/true/recording. It is a hidden
	// live-stack test seam; production deployments must leave it unset.
	FakeProviderEnvVar = "LOOM_CONNECTOR_FAKE_PROVIDER"
	// FakeProviderRecordPathEnvVar optionally appends recording-provider
	// calls as JSONL so external e2e harnesses can inspect live serve egress.
	FakeProviderRecordPathEnvVar = "LOOM_CONNECTOR_FAKE_PROVIDER_RECORD_PATH"
)

// DefaultProviderRegistry builds the standard provider registry — GitHub,
// Slack, and Datadog adapters against their production base URLs (or the
// per-provider LOOM_CONNECTOR_*_BASE_URL overrides) — on the given HTTP
// client. Serve's connector wiring uses this so callers outside this package
// never assemble provider sets themselves.
func DefaultProviderRegistry(client *http.Client) *providers.Registry {
	if fakeProviderEnabled() {
		return fakeProviderRegistry()
	}
	registry := providers.NewRegistry()
	// Register errors only on nil providers or duplicate kinds; neither can
	// happen on this freshly built registry.
	_ = registry.Register(domain.ConnectorSourceGitHub, providers.NewGitHub(client, baseURLOverride(GitHubBaseURLEnvVar)))
	_ = registry.Register(domain.ConnectorSourceSlack, providers.NewSlack(client, baseURLOverride(SlackBaseURLEnvVar)))
	_ = registry.Register(domain.ConnectorSourceDatadog, providers.NewDatadog(client, baseURLOverride(DatadogBaseURLEnvVar)))
	return registry
}

// baseURLOverride reads a provider base-URL env seam; empty means "use the
// provider's production default" (each constructor handles the fallback).
func baseURLOverride(envVar string) string {
	return strings.TrimSpace(os.Getenv(envVar))
}
