package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	dispatchWS         = "ws-1"
	dispatchRun        = "run-1"
	dispatchBinding    = "bind-1"
	dispatchConn       = "gh-main"
	dispatchCredential = "ghp_dispatch_secret_token_1234"
)

// fakeProvider records every CallSpec it receives and replays a canned
// result/error pair.
type fakeProvider struct {
	mu     sync.Mutex
	calls  []providers.CallSpec
	result providers.CallResult
	err    error
}

func (f *fakeProvider) Call(_ context.Context, spec providers.CallSpec) (providers.CallResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, spec)
	return f.result, f.err
}

func (f *fakeProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeProvider) lastCall(t *testing.T) providers.CallSpec {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("provider was never called")
	}
	return f.calls[len(f.calls)-1]
}

// countingConnectorStore counts privileged credential resolutions so deny
// paths can assert the vault path was never approached.
type countingConnectorStore struct {
	store.ConnectorStore
	resolves atomic.Int64
}

func (c *countingConnectorStore) ResolveOutboundCredentialSealed(ctx context.Context, ws, id string) ([]byte, error) {
	c.resolves.Add(1)
	return c.ConnectorStore.ResolveOutboundCredentialSealed(ctx, ws, id)
}

// countingSealer counts Unseal invocations.
type countingSealer struct {
	Sealer
	unseals atomic.Int64
}

func (c *countingSealer) Unseal(sealed, aad []byte) ([]byte, error) {
	c.unseals.Add(1)
	return c.Sealer.Unseal(sealed, aad)
}

type dispatchHarness struct {
	ms       *memstore.Store
	conns    *countingConnectorStore
	sealer   *countingSealer
	provider *fakeProvider
	d        *Dispatcher
}

// newDispatchHarness builds a Dispatcher over memstore with one active
// github connector (sealed credential) and a fake github provider.
func newDispatchHarness(t *testing.T) *dispatchHarness {
	t.Helper()
	ctx := context.Background()
	ms := memstore.New()

	vault, err := NewVault([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	sealed, err := vault.Seal([]byte(dispatchCredential), CredentialAAD(dispatchWS, dispatchConn))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := ms.Connectors().Create(ctx, store.ConnectorCreate{
		WorkspaceKey:             dispatchWS,
		ConnectorID:              dispatchConn,
		SourceKind:               domain.ConnectorSourceGitHub,
		OutboundCredentialSealed: sealed,
	}); err != nil {
		t.Fatalf("create connector: %v", err)
	}

	provider := &fakeProvider{result: providers.CallResult{
		Status:   200,
		Body:     map[string]any{"merged": true},
		Decision: domain.ConnectorCallGranted,
	}}
	registry := providers.NewRegistry()
	if err := registry.Register(domain.ConnectorSourceGitHub, provider); err != nil {
		t.Fatalf("register provider: %v", err)
	}

	conns := &countingConnectorStore{ConnectorStore: ms.Connectors()}
	sealer := &countingSealer{Sealer: vault}
	return &dispatchHarness{
		ms:       ms,
		conns:    conns,
		sealer:   sealer,
		provider: provider,
		d: &Dispatcher{
			Connectors: conns,
			Grants:     ms.ConnectorGrants(),
			Audit:      ms.ConnectorCalls(),
			Vault:      sealer,
			Providers:  registry,
		},
	}
}

func (h *dispatchHarness) grant(t *testing.T, grantID, action, pattern string) {
	t.Helper()
	if _, err := h.ms.ConnectorGrants().Create(context.Background(), store.ConnectorGrantCreate{
		WorkspaceKey:    dispatchWS,
		GrantID:         grantID,
		ConnectorID:     dispatchConn,
		BindingID:       dispatchBinding,
		Action:          action,
		ResourcePattern: pattern,
	}); err != nil {
		t.Fatalf("create grant %s: %v", grantID, err)
	}
}

func (h *dispatchHarness) auditRecords(t *testing.T) []*domain.ConnectorCallRecord {
	t.Helper()
	recs, err := h.ms.ConnectorCalls().ListByRun(context.Background(), dispatchWS, dispatchRun, store.ConnectorCallFilter{})
	if err != nil {
		t.Fatalf("ListByRun: %v", err)
	}
	return recs
}

func baseRequest(seq int) Request {
	return Request{
		WorkspaceKey: dispatchWS,
		RunID:        dispatchRun,
		BindingID:    dispatchBinding,
		ConnectorID:  dispatchConn,
		Action:       "github.review.post",
		Resource:     "repo:octocat/hello",
		Args:         map[string]any{"owner": "octocat", "repo": "hello", "number": 7},
		CallSeq:      seq,
	}
}

func TestDispatchAllowPathWritesGrantedAudit(t *testing.T) {
	h := newDispatchHarness(t)
	h.grant(t, "g-1", "github.review.post", "repo:octocat/*")

	res, err := h.d.Dispatch(context.Background(), baseRequest(0))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Decision != connectorsmodule.ConnectorCallGranted || res.Status != 200 {
		t.Fatalf("result = %+v, want granted/200", res)
	}
	wantID := domain.ConnectorCallID(dispatchRun, "github.review.post", 0)
	if res.CallID != wantID {
		t.Fatalf("CallID = %q, want %q", res.CallID, wantID)
	}

	spec := h.provider.lastCall(t)
	if spec.Credential != dispatchCredential {
		t.Fatalf("provider credential = %q, want unsealed plaintext", spec.Credential)
	}
	if spec.IdempotencyKey != wantID {
		t.Fatalf("IdempotencyKey = %q, want %q", spec.IdempotencyKey, wantID)
	}
	if h.sealer.unseals.Load() != 1 {
		t.Fatalf("unseals = %d, want 1", h.sealer.unseals.Load())
	}

	recs := h.auditRecords(t)
	if len(recs) != 1 {
		t.Fatalf("audit records = %d, want 1", len(recs))
	}
	rec := recs[0]
	if rec.Decision != domain.ConnectorCallGranted || rec.UpstreamStatus != 200 ||
		rec.CallID != wantID || rec.ConnectorID != dispatchConn ||
		rec.SourceKind != domain.ConnectorSourceGitHub || rec.BindingID != dispatchBinding {
		t.Fatalf("audit record = %+v", rec)
	}
	if strings.Contains(rec.SanitizedSummary, dispatchCredential) {
		t.Fatal("audit summary contains credential")
	}
}

func TestDispatchDenyPaths(t *testing.T) {
	cases := []struct {
		name    string
		seed    func(t *testing.T, h *dispatchHarness)
		req     func() Request
		wantIs  []error
		summary string
	}{
		{
			name:   "no grants at all",
			seed:   func(*testing.T, *dispatchHarness) {},
			req:    func() Request { return baseRequest(0) },
			wantIs: []error{domain.ErrGrantDenied},
		},
		{
			name: "action not granted",
			seed: func(t *testing.T, h *dispatchHarness) {
				h.grant(t, "g-1", "github.pull_request.read", "repo:octocat/*")
			},
			req:    func() Request { return baseRequest(0) },
			wantIs: []error{domain.ErrGrantDenied},
		},
		{
			name: "resource not granted",
			seed: func(t *testing.T, h *dispatchHarness) {
				h.grant(t, "g-1", "github.review.post", "repo:other/*")
			},
			req:    func() Request { return baseRequest(0) },
			wantIs: []error{domain.ErrGrantDenied},
		},
		{
			name: "grant on another connector never authorizes",
			seed: func(t *testing.T, h *dispatchHarness) {
				if _, err := h.ms.ConnectorGrants().Create(context.Background(), store.ConnectorGrantCreate{
					WorkspaceKey:    dispatchWS,
					GrantID:         "g-other",
					ConnectorID:     "gh-secondary",
					BindingID:       dispatchBinding,
					Action:          "github.review.post",
					ResourcePattern: "repo:octocat/*",
				}); err != nil {
					t.Fatalf("create grant: %v", err)
				}
			},
			req:    func() Request { return baseRequest(0) },
			wantIs: []error{domain.ErrGrantDenied},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newDispatchHarness(t)
			tc.seed(t, h)

			res, err := h.d.Dispatch(context.Background(), tc.req())
			if err == nil {
				t.Fatal("Dispatch succeeded, want deny")
			}
			for _, want := range tc.wantIs {
				if !errors.Is(err, want) {
					t.Fatalf("err = %v, want errors.Is %v", err, want)
				}
			}
			if res.Decision != connectorsmodule.ConnectorCallDenied {
				t.Fatalf("Decision = %q, want denied", res.Decision)
			}
			// Deny is journaled BEFORE return and never touches the
			// credential path or the provider.
			recs := h.auditRecords(t)
			if len(recs) != 1 || recs[0].Decision != domain.ConnectorCallDenied {
				t.Fatalf("audit records = %+v, want one denied", recs)
			}
			if recs[0].UpstreamStatus != 0 {
				t.Fatalf("UpstreamStatus = %d, want 0 (no egress)", recs[0].UpstreamStatus)
			}
			if h.provider.callCount() != 0 {
				t.Fatal("provider was called on deny path")
			}
			if h.conns.resolves.Load() != 0 {
				t.Fatal("credential resolved on deny path")
			}
			if h.sealer.unseals.Load() != 0 {
				t.Fatal("vault unsealed on deny path")
			}
		})
	}
}

func TestDispatchIrreversibleWithoutPreconditionRefused(t *testing.T) {
	h := newDispatchHarness(t)
	h.grant(t, "g-1", "github.merge", "repo:octocat/*")

	req := baseRequest(0)
	req.Action = "github.merge"
	res, err := h.d.Dispatch(context.Background(), req)
	if err == nil {
		t.Fatal("Dispatch succeeded, want precondition refusal")
	}
	var pre *providers.PreconditionRequired
	if !errors.As(err, &pre) || !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("err = %v, want PreconditionRequired wrapping ErrInvalid", err)
	}
	if len(pre.Fields) != 1 || pre.Fields[0] != "expectedHeadSha" {
		t.Fatalf("missing fields = %v, want [expectedHeadSha]", pre.Fields)
	}
	if res.Decision != connectorsmodule.ConnectorCallPreconditionRequired {
		t.Fatalf("Decision = %q, want precondition_required", res.Decision)
	}
	recs := h.auditRecords(t)
	if len(recs) != 1 || recs[0].Decision != domain.ConnectorCallPreconditionRequired {
		t.Fatalf("audit records = %+v, want one precondition_required", recs)
	}
	if h.provider.callCount() != 0 || h.conns.resolves.Load() != 0 {
		t.Fatal("irreversible refusal must precede credential and provider access")
	}
}

func TestDispatchIrreversibleWithPreconditionEgresses(t *testing.T) {
	h := newDispatchHarness(t)
	h.grant(t, "g-1", "github.merge", "repo:octocat/*")

	req := baseRequest(0)
	req.Action = "github.merge"
	req.Preconditions = providers.Preconditions{ExpectedHeadSha: "abc123"}
	if _, err := h.d.Dispatch(context.Background(), req); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	spec := h.provider.lastCall(t)
	if spec.Preconditions.ExpectedHeadSha != "abc123" {
		t.Fatalf("provider preconditions = %+v", spec.Preconditions)
	}
}

func TestDispatchStaleSubjectAudited(t *testing.T) {
	h := newDispatchHarness(t)
	h.grant(t, "g-1", "github.merge", "repo:octocat/*")
	h.provider.result = providers.CallResult{Status: 409, Decision: domain.ConnectorCallStaleSubject}
	h.provider.err = &providers.StaleSubject{
		Action: "github.merge", Resource: "repo:octocat/hello",
		Expected: "abc123", Reason: "head moved",
	}

	req := baseRequest(0)
	req.Action = "github.merge"
	req.Preconditions = providers.Preconditions{ExpectedHeadSha: "abc123"}
	res, err := h.d.Dispatch(context.Background(), req)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want errors.Is ErrConflict", err)
	}
	if res.Decision != connectorsmodule.ConnectorCallStaleSubject || res.Status != 409 {
		t.Fatalf("result = %+v, want stale_subject/409", res)
	}
	recs := h.auditRecords(t)
	if len(recs) != 1 || recs[0].Decision != domain.ConnectorCallStaleSubject || recs[0].UpstreamStatus != 409 {
		t.Fatalf("audit records = %+v, want one stale_subject/409", recs)
	}
}

func TestDispatchUpstreamErrorAudited(t *testing.T) {
	h := newDispatchHarness(t)
	h.grant(t, "g-1", "github.review.post", "repo:octocat/*")
	h.provider.result = providers.CallResult{Status: 500, Decision: domain.ConnectorCallUpstreamError}
	h.provider.err = &providers.UpstreamError{
		Action: "github.review.post", Class: providers.ClassServerError, Status: 500,
	}

	res, err := h.d.Dispatch(context.Background(), baseRequest(0))
	if !errors.Is(err, providers.ErrUpstream) {
		t.Fatalf("err = %v, want errors.Is ErrUpstream", err)
	}
	if !providers.Retryable(err) {
		t.Fatal("server error should signal retryable upward")
	}
	if res.Decision != connectorsmodule.ConnectorCallUpstreamError {
		t.Fatalf("Decision = %q, want upstream_error", res.Decision)
	}
	recs := h.auditRecords(t)
	if len(recs) != 1 || recs[0].ErrorClass != providers.ClassServerError || recs[0].UpstreamStatus != 500 {
		t.Fatalf("audit records = %+v, want one server_error/500", recs)
	}
}

func TestDispatchRevokedGrantMidFlightDenied(t *testing.T) {
	h := newDispatchHarness(t)
	h.grant(t, "g-1", "github.review.post", "repo:octocat/*")
	ctx := context.Background()

	if _, err := h.d.Dispatch(ctx, baseRequest(0)); err != nil {
		t.Fatalf("first Dispatch: %v", err)
	}
	if err := h.ms.ConnectorGrants().Revoke(ctx, dispatchWS, "g-1"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	// Grants are re-resolved at call time, never cached: the next call on
	// the same dispatcher must deny.
	_, err := h.d.Dispatch(ctx, baseRequest(1))
	if !errors.Is(err, domain.ErrGrantDenied) {
		t.Fatalf("err = %v, want errors.Is ErrGrantDenied", err)
	}
	if h.provider.callCount() != 1 {
		t.Fatalf("provider calls = %d, want 1 (revoked call refused)", h.provider.callCount())
	}
	recs := h.auditRecords(t)
	if len(recs) != 2 || recs[0].Decision != domain.ConnectorCallGranted || recs[1].Decision != domain.ConnectorCallDenied {
		t.Fatalf("audit records = %+v, want granted then denied", recs)
	}
}

func TestDispatchAuditIdempotentOnDuplicateDispatch(t *testing.T) {
	h := newDispatchHarness(t)
	h.grant(t, "g-1", "github.review.post", "repo:octocat/*")
	ctx := context.Background()

	// Same RunID/Action/CallSeq twice: a task retry. The duplicate audit
	// append is swallowed and the journal keeps exactly one row.
	for i := 0; i < 2; i++ {
		if _, err := h.d.Dispatch(ctx, baseRequest(0)); err != nil {
			t.Fatalf("Dispatch attempt %d: %v", i, err)
		}
	}
	if h.provider.callCount() != 2 {
		t.Fatalf("provider calls = %d, want 2 (upstream dedups via Idempotency-Key)", h.provider.callCount())
	}
	recs := h.auditRecords(t)
	if len(recs) != 1 {
		t.Fatalf("audit records = %d, want 1", len(recs))
	}
}

func TestDispatchConnectorNotFound(t *testing.T) {
	h := newDispatchHarness(t)
	req := baseRequest(0)
	req.ConnectorID = "missing"
	_, err := h.d.Dispatch(context.Background(), req)
	if !errors.Is(err, domain.ErrConnectorNotFound) {
		t.Fatalf("err = %v, want errors.Is ErrConnectorNotFound", err)
	}
	if got := len(h.auditRecords(t)); got != 0 {
		t.Fatalf("audit records = %d, want 0 (no valid record possible)", got)
	}
}

func TestDispatchDisabledConnectorRefused(t *testing.T) {
	h := newDispatchHarness(t)
	ctx := context.Background()
	if _, err := h.ms.Connectors().Create(ctx, store.ConnectorCreate{
		WorkspaceKey: dispatchWS,
		ConnectorID:  "gh-off",
		SourceKind:   domain.ConnectorSourceGitHub,
		Status:       domain.ConnectorStatusDisabled,
	}); err != nil {
		t.Fatalf("create connector: %v", err)
	}
	req := baseRequest(0)
	req.ConnectorID = "gh-off"
	res, err := h.d.Dispatch(ctx, req)
	if !errors.Is(err, ErrConnectorDisabled) || !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("err = %v, want ErrConnectorDisabled wrapping ErrInvalid", err)
	}
	if res.Decision != connectorsmodule.ConnectorCallDenied {
		t.Fatalf("Decision = %q, want denied", res.Decision)
	}
	if recs := h.auditRecords(t); len(recs) != 1 || recs[0].Decision != domain.ConnectorCallDenied {
		t.Fatalf("audit records = %+v, want one denied", recs)
	}
}

func TestDispatchMissingCredentialRefused(t *testing.T) {
	h := newDispatchHarness(t)
	ctx := context.Background()
	if _, err := h.ms.Connectors().Create(ctx, store.ConnectorCreate{
		WorkspaceKey: dispatchWS,
		ConnectorID:  "gh-nocred",
		SourceKind:   domain.ConnectorSourceGitHub,
	}); err != nil {
		t.Fatalf("create connector: %v", err)
	}
	if _, err := h.ms.ConnectorGrants().Create(ctx, store.ConnectorGrantCreate{
		WorkspaceKey:    dispatchWS,
		GrantID:         "g-nocred",
		ConnectorID:     "gh-nocred",
		BindingID:       dispatchBinding,
		Action:          "github.review.post",
		ResourcePattern: "repo:octocat/*",
	}); err != nil {
		t.Fatalf("create grant: %v", err)
	}
	req := baseRequest(0)
	req.ConnectorID = "gh-nocred"
	_, err := h.d.Dispatch(ctx, req)
	if !errors.Is(err, ErrNoOutboundCredential) {
		t.Fatalf("err = %v, want ErrNoOutboundCredential", err)
	}
	if h.provider.callCount() != 0 {
		t.Fatal("provider called without a credential")
	}
	if recs := h.auditRecords(t); len(recs) != 1 || recs[0].Decision != domain.ConnectorCallDenied {
		t.Fatalf("audit records = %+v, want one denied", recs)
	}
}

func TestDispatchUnsealFailureRefused(t *testing.T) {
	h := newDispatchHarness(t)
	ctx := context.Background()
	// Ciphertext sealed under a mismatched AAD (different connector id)
	// must fail authentication at dispatch time.
	spliced, err := h.sealer.Seal([]byte("stolen"), CredentialAAD(dispatchWS, "some-other-connector"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := h.ms.Connectors().Create(ctx, store.ConnectorCreate{
		WorkspaceKey:             dispatchWS,
		ConnectorID:              "gh-spliced",
		SourceKind:               domain.ConnectorSourceGitHub,
		OutboundCredentialSealed: spliced,
	}); err != nil {
		t.Fatalf("create connector: %v", err)
	}
	if _, err := h.ms.ConnectorGrants().Create(ctx, store.ConnectorGrantCreate{
		WorkspaceKey:    dispatchWS,
		GrantID:         "g-spliced",
		ConnectorID:     "gh-spliced",
		BindingID:       dispatchBinding,
		Action:          "github.review.post",
		ResourcePattern: "repo:octocat/*",
	}); err != nil {
		t.Fatalf("create grant: %v", err)
	}
	req := baseRequest(0)
	req.ConnectorID = "gh-spliced"
	_, err = h.d.Dispatch(ctx, req)
	if !errors.Is(err, ErrUnseal) {
		t.Fatalf("err = %v, want errors.Is ErrUnseal", err)
	}
	if h.provider.callCount() != 0 {
		t.Fatal("provider called after unseal failure")
	}
	if recs := h.auditRecords(t); len(recs) != 1 || recs[0].Decision != domain.ConnectorCallDenied {
		t.Fatalf("audit records = %+v, want one denied", recs)
	}
}

func TestDispatchNoProviderForKindRefused(t *testing.T) {
	h := newDispatchHarness(t)
	ctx := context.Background()
	if _, err := h.ms.Connectors().Create(ctx, store.ConnectorCreate{
		WorkspaceKey: dispatchWS,
		ConnectorID:  "slack-main",
		SourceKind:   domain.ConnectorSourceSlack,
	}); err != nil {
		t.Fatalf("create connector: %v", err)
	}
	if _, err := h.ms.ConnectorGrants().Create(ctx, store.ConnectorGrantCreate{
		WorkspaceKey:    dispatchWS,
		GrantID:         "g-slack",
		ConnectorID:     "slack-main",
		BindingID:       dispatchBinding,
		Action:          "slack.chat.post_message",
		ResourcePattern: "channel:general",
	}); err != nil {
		t.Fatalf("create grant: %v", err)
	}
	req := baseRequest(0)
	req.ConnectorID = "slack-main"
	req.Action = "slack.chat.post_message"
	req.Resource = "channel:general"
	_, err := h.d.Dispatch(ctx, req)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want errors.Is ErrNotFound", err)
	}
	if h.conns.resolves.Load() != 0 {
		t.Fatal("credential resolved despite missing provider")
	}
	if recs := h.auditRecords(t); len(recs) != 1 || recs[0].Decision != domain.ConnectorCallDenied {
		t.Fatalf("audit records = %+v, want one denied", recs)
	}
}

func TestDispatchRequestValidation(t *testing.T) {
	mutate := []struct {
		name string
		mod  func(*Request)
	}{
		{"empty workspace", func(r *Request) { r.WorkspaceKey = "" }},
		{"empty run", func(r *Request) { r.RunID = "" }},
		{"empty binding", func(r *Request) { r.BindingID = "" }},
		{"empty connector", func(r *Request) { r.ConnectorID = "" }},
		{"bad action", func(r *Request) { r.Action = "merge" }},
		{"action bad char", func(r *Request) { r.Action = "github.Merge" }},
		{"empty resource", func(r *Request) { r.Resource = "" }},
		{"negative seq", func(r *Request) { r.CallSeq = -1 }},
	}
	for _, tc := range mutate {
		t.Run(tc.name, func(t *testing.T) {
			h := newDispatchHarness(t)
			h.grant(t, "g-1", "github.review.post", "repo:octocat/*")
			req := baseRequest(0)
			tc.mod(&req)
			_, err := h.d.Dispatch(context.Background(), req)
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("err = %v, want errors.Is ErrInvalid", err)
			}
			if h.provider.callCount() != 0 || h.conns.resolves.Load() != 0 {
				t.Fatal("malformed request reached provider or credential path")
			}
		})
	}
}

func TestDispatchConcurrentCallsOnOneRun(t *testing.T) {
	h := newDispatchHarness(t)
	h.grant(t, "g-1", "github.review.post", "repo:octocat/*")
	ctx := context.Background()

	const n = 16
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			_, errs[seq] = h.d.Dispatch(ctx, baseRequest(seq))
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Dispatch seq %d: %v", i, err)
		}
	}
	if h.provider.callCount() != n {
		t.Fatalf("provider calls = %d, want %d", h.provider.callCount(), n)
	}
	recs := h.auditRecords(t)
	if len(recs) != n {
		t.Fatalf("audit records = %d, want %d", len(recs), n)
	}
	seen := make(map[string]bool, n)
	for _, rec := range recs {
		if rec.Decision != domain.ConnectorCallGranted {
			t.Fatalf("record %s decision = %q", rec.CallID, rec.Decision)
		}
		if seen[rec.CallID] {
			t.Fatalf("duplicate CallID %s", rec.CallID)
		}
		seen[rec.CallID] = true
	}
	for i := 0; i < n; i++ {
		if id := domain.ConnectorCallID(dispatchRun, "github.review.post", i); !seen[id] {
			t.Fatalf("missing CallID %s", id)
		}
	}
}

func TestDispatchPreconditionFieldMapping(t *testing.T) {
	if got := connectorsmodule.MissingActionPreconditions("github.pull_request.read", providers.Preconditions{}); got != nil {
		t.Fatalf("reversible action missing = %v, want nil", got)
	}
	if got := connectorsmodule.MissingActionPreconditions("github.merge", providers.Preconditions{}); len(got) != 1 || got[0] != "expectedHeadSha" {
		t.Fatalf("github.merge missing = %v, want [expectedHeadSha]", got)
	}
}

func TestDispatchSummaryCapped(t *testing.T) {
	h := newDispatchHarness(t)
	h.grant(t, "g-1", "github.review.post", "repo:octocat/*")
	h.provider.result = providers.CallResult{Status: 502, Decision: domain.ConnectorCallUpstreamError}
	h.provider.err = &providers.UpstreamError{
		Action: "github.review.post", Class: providers.ClassServerError, Status: 502,
		Summary: strings.Repeat("x", 3*maxSummaryLen),
	}
	if _, err := h.d.Dispatch(context.Background(), baseRequest(0)); err == nil {
		t.Fatal("Dispatch succeeded, want upstream error")
	}
	recs := h.auditRecords(t)
	if len(recs) != 1 {
		t.Fatalf("audit records = %d, want 1", len(recs))
	}
	if got := len(recs[0].SanitizedSummary); got > maxSummaryLen+len("...") {
		t.Fatalf("summary length = %d, want <= %d", got, maxSummaryLen+3)
	}
}

// errJournalDown simulates an audit-store outage.
var errJournalDown = errors.New("journal unavailable")

// errorAudit fails every Append with a non-duplicate error to prove the deny
// path joins the audit failure onto the refusal instead of masking it.
type errorAudit struct {
	store.ConnectorAuditStore
}

func (e *errorAudit) Append(context.Context, *domain.ConnectorCallRecord) error {
	return fmt.Errorf("append: %w", errJournalDown)
}

func TestDispatchDenyAuditFailureJoined(t *testing.T) {
	h := newDispatchHarness(t)
	h.d.Audit = &errorAudit{ConnectorAuditStore: h.ms.ConnectorCalls()}
	_, err := h.d.Dispatch(context.Background(), baseRequest(0))
	if !errors.Is(err, domain.ErrGrantDenied) {
		t.Fatalf("err = %v, want deny preserved", err)
	}
	if !errors.Is(err, errJournalDown) {
		t.Fatalf("err = %v, want audit failure joined", err)
	}
}
