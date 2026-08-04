package connectors

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type dispatchStoreFake struct {
	connector *Connector
	sealed    []byte
	grants    []*ConnectorGrant
	audits    []*ConnectorCallRecord
	appendErr error
}

func (store *dispatchStoreFake) GetConnectorRecord(context.Context, string, string) (*Connector, error) {
	return store.connector, nil
}

func (store *dispatchStoreFake) ResolveOutboundCredentialSealedRecord(context.Context, string, string) ([]byte, error) {
	return append([]byte(nil), store.sealed...), nil
}

func (store *dispatchStoreFake) ListGrantRecordsByBinding(context.Context, string, string) ([]*ConnectorGrant, error) {
	return append([]*ConnectorGrant(nil), store.grants...), nil
}

func (store *dispatchStoreFake) AppendConnectorCallRecord(_ context.Context, record *ConnectorCallRecord) error {
	copy := *record
	store.audits = append(store.audits, &copy)
	return store.appendErr
}

type credentialOpenerFake struct {
	plaintext []byte
	calls     int
	aad       []byte
}

func (vault *credentialOpenerFake) Unseal(_ []byte, aad []byte) ([]byte, error) {
	vault.calls++
	vault.aad = append([]byte(nil), aad...)
	return vault.plaintext, nil
}

type providerRegistryFake struct {
	provider Provider
	gets     int
}

func (registry *providerRegistryFake) Get(ConnectorSourceKind) (Provider, error) {
	registry.gets++
	if registry.provider == nil {
		return nil, ErrNotFound
	}
	return registry.provider, nil
}

type providerFake struct {
	call   ProviderCall
	calls  int
	result ProviderResult
	err    error
}

func (provider *providerFake) Call(_ context.Context, call ProviderCall) (ProviderResult, error) {
	provider.calls++
	provider.call = call
	return provider.result, provider.err
}

func newDispatchServiceHarness(t *testing.T) (*DispatchService, *dispatchStoreFake, *credentialOpenerFake, *providerRegistryFake, *providerFake) {
	t.Helper()
	store := &dispatchStoreFake{
		connector: &Connector{
			WorkspaceKey: "WS", ConnectorID: "github-main", SourceKind: ConnectorSourceGitHub,
			Status: ConnectorStatusActive,
		},
		sealed: []byte("sealed"),
		grants: []*ConnectorGrant{{
			WorkspaceKey: "WS", GrantID: "grant-1", ConnectorID: "github-main",
			BindingID: "binding-1", Action: ActionGitHubReviewPost, ResourcePattern: "repo:octocat/hello",
		}},
	}
	vault := &credentialOpenerFake{plaintext: []byte("top-secret")}
	provider := &providerFake{result: ProviderResult{
		Status: 201, Body: map[string]any{"created": true}, Decision: ConnectorCallGranted,
	}}
	registry := &providerRegistryFake{provider: provider}
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.FixedZone("PDT", -7*60*60))
	service, err := NewDispatch(store, vault, registry, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return service, store, vault, registry, provider
}

func validDispatchCommand() DispatchCommand {
	return DispatchCommand{
		WorkspaceKey: "WS", RunID: "run-1", BindingID: "binding-1", ConnectorID: "github-main",
		Action: ActionGitHubReviewPost, Resource: "repo:octocat/hello", Args: map[string]any{"number": 42}, CallSeq: 3,
	}
}

func TestDispatchServiceAuthorizesUnsealsCallsAndAudits(t *testing.T) {
	service, store, vault, registry, provider := newDispatchServiceHarness(t)
	result, err := service.Dispatch(t.Context(), validDispatchCommand())
	if err != nil {
		t.Fatal(err)
	}
	if result.CallID != "run-1#github.review.post#3" || result.Decision != ConnectorCallGranted || result.Status != 201 {
		t.Fatalf("Dispatch result = %+v", result)
	}
	if registry.gets != 1 || vault.calls != 1 || provider.calls != 1 {
		t.Fatalf("calls: registry=%d vault=%d provider=%d", registry.gets, vault.calls, provider.calls)
	}
	if provider.call.Credential != "top-secret" || provider.call.IdempotencyKey != result.CallID {
		t.Fatalf("provider call = %+v", provider.call)
	}
	if !reflect.DeepEqual(vault.aad, CredentialAAD("WS", "github-main")) {
		t.Fatalf("vault AAD = %q", vault.aad)
	}
	if !reflect.DeepEqual(vault.plaintext, make([]byte, len(vault.plaintext))) {
		t.Fatalf("plaintext was not wiped after provider call: %v", vault.plaintext)
	}
	if len(store.audits) != 1 {
		t.Fatalf("audit count = %d", len(store.audits))
	}
	audit := store.audits[0]
	if audit.CallID != result.CallID || audit.Decision != ConnectorCallGranted || audit.UpstreamStatus != 201 || audit.OccurredAt.Location() != time.UTC {
		t.Fatalf("audit = %+v", audit)
	}
}

func TestDispatchServiceDeniesBeforeProviderOrCredential(t *testing.T) {
	service, store, vault, registry, provider := newDispatchServiceHarness(t)
	store.grants = nil
	result, err := service.Dispatch(t.Context(), validDispatchCommand())
	if !errors.Is(err, ErrGrantDenied) || result.Decision != ConnectorCallDenied {
		t.Fatalf("Dispatch = %+v, %v", result, err)
	}
	if registry.gets != 0 || vault.calls != 0 || provider.calls != 0 {
		t.Fatalf("denied call reached egress: registry=%d vault=%d provider=%d", registry.gets, vault.calls, provider.calls)
	}
	if len(store.audits) != 1 || store.audits[0].Decision != ConnectorCallDenied {
		t.Fatalf("denial audits = %+v", store.audits)
	}
}

func TestDispatchServiceRequiresIrreversiblePreconditionBeforeEgress(t *testing.T) {
	service, store, vault, registry, provider := newDispatchServiceHarness(t)
	command := validDispatchCommand()
	command.Action = ActionGitHubMerge
	store.grants[0].Action = ActionGitHubMerge
	result, err := service.Dispatch(t.Context(), command)
	var required *PreconditionRequired
	if !errors.As(err, &required) || result.Decision != ConnectorCallPreconditionRequired {
		t.Fatalf("Dispatch = %+v, %v", result, err)
	}
	if !reflect.DeepEqual(required.Fields, []string{"expectedHeadSha"}) {
		t.Fatalf("required fields = %v", required.Fields)
	}
	if registry.gets != 0 || vault.calls != 0 || provider.calls != 0 {
		t.Fatalf("precondition refusal reached egress: registry=%d vault=%d provider=%d", registry.gets, vault.calls, provider.calls)
	}
}

type upstreamDispatchError struct{}

func (*upstreamDispatchError) Error() string { return "upstream timed out" }

func (*upstreamDispatchError) ConnectorFailure() DispatchFailure {
	return DispatchFailure{Kind: DispatchFailureUpstream, Retryable: true, ErrorClass: "timeout"}
}

func TestDispatchServiceClassifiesAndAuditsProviderFailure(t *testing.T) {
	service, store, _, _, provider := newDispatchServiceHarness(t)
	provider.result = ProviderResult{Status: 503}
	provider.err = &upstreamDispatchError{}
	result, err := service.Dispatch(t.Context(), validDispatchCommand())
	if err == nil || result.Decision != ConnectorCallUpstreamError || result.Status != 503 {
		t.Fatalf("Dispatch = %+v, %v", result, err)
	}
	if len(store.audits) != 1 || store.audits[0].Decision != ConnectorCallUpstreamError || store.audits[0].ErrorClass != "timeout" {
		t.Fatalf("failure audits = %+v", store.audits)
	}
}

func TestDispatchServiceTreatsDuplicateAuditAsReplaySuccess(t *testing.T) {
	service, store, _, _, _ := newDispatchServiceHarness(t)
	store.appendErr = ErrAlreadyExists
	result, err := service.Dispatch(t.Context(), validDispatchCommand())
	if err != nil || result.Decision != ConnectorCallGranted {
		t.Fatalf("Dispatch replay = %+v, %v", result, err)
	}
}
