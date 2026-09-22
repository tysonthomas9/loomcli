package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// RecordingProviderCall is a redaction-safe record of one fake-provider call.
// It intentionally records credential presence, not credential material.
type RecordingProviderCall struct {
	SourceKind          string   `json:"source_kind"`
	Action              string   `json:"action"`
	Resource            string   `json:"resource"`
	IdempotencyKey      string   `json:"idempotency_key,omitempty"`
	CredentialPresented bool     `json:"credential_presented"`
	ArgKeys             []string `json:"arg_keys,omitempty"`
	ExpectedHeadSHA     string   `json:"expected_head_sha,omitempty"`
	ExpectedRevision    string   `json:"expected_revision,omitempty"`
}

var recordingProviderCalls = struct {
	sync.Mutex
	calls []RecordingProviderCall
}{}

// ResetRecordingProviderCalls clears the in-process fake-provider call log.
// Tests using LOOM_CONNECTOR_FAKE_PROVIDER should call this before assertions.
func ResetRecordingProviderCalls() {
	recordingProviderCalls.Lock()
	defer recordingProviderCalls.Unlock()
	recordingProviderCalls.calls = nil
}

// RecordingProviderCalls returns a snapshot of fake-provider calls observed
// in this process.
func RecordingProviderCalls() []RecordingProviderCall {
	recordingProviderCalls.Lock()
	defer recordingProviderCalls.Unlock()
	return append([]RecordingProviderCall(nil), recordingProviderCalls.calls...)
}

type recordingProvider struct {
	kind    domain.ConnectorSourceKind
	actions map[string]struct{}
}

func newRecordingProvider(kind domain.ConnectorSourceKind, actions []string) *recordingProvider {
	allowed := make(map[string]struct{}, len(actions))
	for _, action := range actions {
		allowed[action] = struct{}{}
	}
	return &recordingProvider{kind: kind, actions: allowed}
}

func (p *recordingProvider) Call(_ context.Context, spec providers.CallSpec) (providers.CallResult, error) {
	if _, ok := p.actions[spec.Action]; !ok {
		return providers.CallResult{Decision: domain.ConnectorCallUpstreamError},
			fmt.Errorf("%s fake provider does not implement %q: %w", p.kind, spec.Action, providers.ErrUnknownAction)
	}
	if result, err := validateRecordingProviderCall(spec); err != nil {
		return result, err
	}
	rec := RecordingProviderCall{
		SourceKind:          string(p.kind),
		Action:              spec.Action,
		Resource:            spec.Resource,
		IdempotencyKey:      spec.IdempotencyKey,
		CredentialPresented: spec.Credential != "",
		ArgKeys:             sortedArgKeys(spec.Args),
		ExpectedHeadSHA:     spec.Preconditions.ExpectedHeadSha,
		ExpectedRevision:    spec.Preconditions.ExpectedRevision,
	}
	if err := recordFakeProviderCall(rec); err != nil {
		return providers.CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	return providers.CallResult{
		Status:   http.StatusOK,
		Decision: domain.ConnectorCallGranted,
		Body: map[string]any{
			"fakeProvider":        true,
			"sourceKind":          string(p.kind),
			"action":              spec.Action,
			"resource":            spec.Resource,
			"idempotencyKey":      spec.IdempotencyKey,
			"credentialPresented": spec.Credential != "",
		},
	}, nil
}

func validateRecordingProviderCall(spec providers.CallSpec) (providers.CallResult, error) {
	switch spec.Action {
	case providers.ActionGitHubMerge, providers.ActionGitHubReviewPost:
		if strings.TrimSpace(spec.Preconditions.ExpectedHeadSha) == "" {
			return providers.CallResult{Decision: domain.ConnectorCallPreconditionRequired},
				&providers.PreconditionRequired{Action: spec.Action, Fields: []string{"expectedHeadSha"}}
		}
	case providers.ActionDatadogIncidentsWrite:
		if !hasArg(spec.Args, "monitorId") {
			return providers.CallResult{Decision: domain.ConnectorCallPreconditionRequired},
				&providers.PreconditionRequired{Action: spec.Action, Fields: []string{"monitorId"}}
		}
	}
	return providers.CallResult{}, nil
}

func fakeProviderEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(FakeProviderEnvVar))) {
	case "1", "true", "recording":
		return true
	default:
		return false
	}
}

func fakeProviderRegistry() *providers.Registry {
	registry := providers.NewRegistry()
	_ = registry.Register(domain.ConnectorSourceGitHub,
		newRecordingProvider(domain.ConnectorSourceGitHub, providers.GitHubActions()))
	_ = registry.Register(domain.ConnectorSourceSlack,
		newRecordingProvider(domain.ConnectorSourceSlack, providers.SlackActions()))
	_ = registry.Register(domain.ConnectorSourceDatadog,
		newRecordingProvider(domain.ConnectorSourceDatadog, providers.DatadogActions()))
	return registry
}

func recordFakeProviderCall(rec RecordingProviderCall) error {
	recordingProviderCalls.Lock()
	defer recordingProviderCalls.Unlock()

	recordingProviderCalls.calls = append(recordingProviderCalls.calls, rec)
	path := strings.TrimSpace(os.Getenv(FakeProviderRecordPathEnvVar))
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // fake-provider record path is explicit test/operator configuration.
	if err != nil {
		return fmt.Errorf("fake connector provider record call: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := json.NewEncoder(f).Encode(rec); err != nil {
		return fmt.Errorf("fake connector provider encode call record: %w", err)
	}
	return nil
}

func hasArg(args map[string]any, key string) bool {
	value, ok := args[key]
	if !ok || value == nil {
		return false
	}
	if s, ok := value.(string); ok && strings.TrimSpace(s) == "" {
		return false
	}
	return true
}

func sortedArgKeys(args map[string]any) []string {
	if len(args) == 0 {
		return nil
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
