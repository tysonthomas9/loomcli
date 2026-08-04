package connectorsproviders

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	legacyproviders "github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

const (
	GitHubBaseURLEnvVar  = "LOOM_CONNECTOR_GITHUB_BASE_URL"
	SlackBaseURLEnvVar   = "LOOM_CONNECTOR_SLACK_BASE_URL"
	DatadogBaseURLEnvVar = "LOOM_CONNECTOR_DATADOG_BASE_URL"
)

type Registry struct {
	legacy *legacyproviders.Registry
}

var _ connectorsmodule.ProviderRegistry = (*Registry)(nil)

func New(legacy *legacyproviders.Registry) (*Registry, error) {
	if legacy == nil {
		return nil, connectorsmodule.ErrUnavailable
	}
	return &Registry{legacy: legacy}, nil
}

func Default(client *http.Client) *Registry {
	legacy := legacyproviders.NewRegistry()
	_ = legacy.Register(
		domain.ConnectorSourceGitHub,
		legacyproviders.NewGitHub(client, baseURLOverride(GitHubBaseURLEnvVar)),
	)
	_ = legacy.Register(
		domain.ConnectorSourceSlack,
		legacyproviders.NewSlack(client, baseURLOverride(SlackBaseURLEnvVar)),
	)
	_ = legacy.Register(
		domain.ConnectorSourceDatadog,
		legacyproviders.NewDatadog(client, baseURLOverride(DatadogBaseURLEnvVar)),
	)
	return &Registry{legacy: legacy}
}

func (registry *Registry) Get(source connectorsmodule.ConnectorSourceKind) (connectorsmodule.Provider, error) {
	if registry == nil || registry.legacy == nil {
		return nil, connectorsmodule.ErrUnavailable
	}
	provider, err := registry.legacy.Get(domain.ConnectorSourceKind(source))
	if err != nil {
		return nil, errorsJoinOwner(err)
	}
	return providerAdapter{provider: provider}, nil
}

type providerAdapter struct {
	provider legacyproviders.Provider
}

func (adapter providerAdapter) Call(
	ctx context.Context,
	call connectorsmodule.ProviderCall,
) (connectorsmodule.ProviderResult, error) {
	if adapter.provider == nil {
		return connectorsmodule.ProviderResult{}, connectorsmodule.ErrUnavailable
	}
	result, err := adapter.provider.Call(ctx, legacyproviders.CallSpec{
		Action: call.Action, Resource: call.Resource, Args: call.Args,
		Preconditions: call.Preconditions, IdempotencyKey: call.IdempotencyKey, Credential: call.Credential,
	})
	return connectorsmodule.ProviderResult{
		Status: result.Status, Body: result.Body,
		Decision: connectorsmodule.ConnectorCallDecision(result.Decision),
	}, err
}

func baseURLOverride(environmentVariable string) string {
	return strings.TrimSpace(os.Getenv(environmentVariable))
}

func errorsJoinOwner(err error) error {
	return fmt.Errorf("connector provider registry: %w: %w", connectorsmodule.ErrNotFound, err)
}
