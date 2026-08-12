package driverapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	providers "github.com/tysonthomas9/loomcli/internal/infra/connectorsproviders"
	"github.com/tysonthomas9/loomcli/internal/infra/connectorsvault"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// connectorTestCredential is the plaintext outbound credential sealed into
// the test vault. The secret-leak scan asserts it never appears in any HTTP
// response or audit row.
const connectorTestCredential = "tok-superSecret123"

// stubProvider records every CallSpec and replays a configured outcome.
type stubProvider struct {
	calls  []providers.CallSpec
	result providers.CallResult
	err    error
}

type unreachableConnectorDispatcher struct{}

func (unreachableConnectorDispatcher) Dispatch(
	context.Context,
	connectorsmodule.DispatchCommand,
) (connectorsmodule.DispatchResult, error) {
	panic("connector dispatch unexpectedly reached")
}

func (p *stubProvider) Call(_ context.Context, spec providers.CallSpec) (providers.CallResult, error) {
	p.calls = append(p.calls, spec)
	return p.result, p.err
}

// connectorHarness wires a trigger-dispatched, claimed driver run plus a
// github connector with grants for github.issues.comment and github.merge on
// repo:octocat/hello, all behind a real Dispatcher with a stub provider.
type connectorHarness struct {
	*testHarness
	provider  *stubProvider
	bindingID string
}

func newConnectorHarness(t *testing.T) *connectorHarness {
	t.Helper()
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "driver-1", Name: "epic-runner",
		OwnerType: workflowcatalog.DriverOwnerSystem, Status: workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "version-1", DriverID: "driver-1", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "binding-1", Name: "PR webhook",
		SourceKind: "github", RouteKey: "github.pull_request.opened",
		DriverID: "driver-1", DriverVersionID: "version-1", Enabled: true,
	}); err != nil {
		t.Fatalf("Create trigger binding: %v", err)
	}
	run, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: "WS", RunID: "run-connector-1",
		DriverID: "driver-1", DriverVersionID: "version-1", Entrypoint: "run",
		SourceKind: "github", SourceRef: "event-connector-1",
		TriggerBindingID: "binding-1", IdempotencyKey: "idem-1",
	})
	if err != nil {
		t.Fatalf("create connector run fixture: %v", err)
	}
	claimed, err := st.DriverRuns().Claim(ctx, "WS", run.RunID, "node-1", "lease-1")
	if err != nil {
		t.Fatalf("Claim driver run: %v", err)
	}

	vault, err := connectorsvault.NewVault(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	sealed, err := vault.Seal([]byte(connectorTestCredential), connectorsmodule.CredentialAAD("WS", "gh-main"))
	if err != nil {
		t.Fatalf("Seal credential: %v", err)
	}
	if _, err := st.Connectors().CreateConnectorRecord(ctx, connectorsmodule.CreateConnectorMutation{
		WorkspaceKey: "WS", ConnectorID: "gh-main",
		SourceKind:               connectorsmodule.ConnectorSourceGitHub,
		OutboundCredentialSealed: sealed,
	}); err != nil {
		t.Fatalf("Create connector: %v", err)
	}
	for i, action := range []string{"github.issues.comment", "github.merge"} {
		if _, err := st.Connectors().CreateManagementGrant(ctx, connectorsmodule.CreateGrantMutation{
			WorkspaceKey: "WS", GrantID: fmt.Sprintf("grant-%d", i+1),
			ConnectorID: "gh-main", BindingID: "binding-1",
			Action: action, ResourcePattern: "repo:octocat/hello",
		}); err != nil {
			t.Fatalf("Create grant %s: %v", action, err)
		}
	}

	provider := &stubProvider{result: providers.CallResult{
		Status:   http.StatusOK,
		Body:     map[string]any{"commentId": "c-1"},
		Decision: connectorsmodule.ConnectorCallGranted,
	}}
	registry := providers.NewRegistry()
	if err := registry.Register(connectorsmodule.ConnectorSourceGitHub, provider); err != nil {
		t.Fatalf("Register provider: %v", err)
	}
	dispatcher, err := connectorsmodule.NewDispatch(st.Connectors(), vault, registry, nil)
	if err != nil {
		t.Fatalf("New connector dispatcher: %v", err)
	}
	runTokenKey := bytes.Repeat([]byte{0x42}, 32)
	module := NewModule(Config{
		RunTokenKey:          runTokenKey,
		Execution:            testDriverRunExecution{store: st},
		ExecutionAuthorities: testDriverRunAuthorityResolver{},
		Dispatcher:           dispatcher,
		AutomationBindings:   testAutomationQueries{store: st},
		AutomationDeliveries: testAutomationQueries{store: st},
	})
	mux := http.NewServeMux()
	module.Register(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &connectorHarness{
		testHarness: &testHarness{
			server:      server,
			store:       st,
			module:      module,
			runID:       claimed.RunID,
			nodeID:      claimed.NodeID,
			leaseID:     claimed.LeaseID,
			fence:       claimed.FencingToken,
			runTokenKey: runTokenKey,
		},
		provider:  provider,
		bindingID: "binding-1",
	}
}

// dispatchBody is the canonical happy-path request body.
func (h *connectorHarness) dispatchBody(action string) map[string]any {
	return map[string]any{
		"connectorId": "gh-main",
		"action":      action,
		"resource":    "repo:octocat/hello",
		"args":        map[string]any{"body": "looks good"},
		"callSeq":     1,
	}
}

// doRaw posts the connector-dispatch op and returns status plus the raw
// response bytes for golden/leak assertions.
func (h *connectorHarness) doRaw(t *testing.T, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/api/workspaces/WS/driver/connector-dispatch", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		if value != "" {
			req.Header.Set(name, value)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp.StatusCode, raw
}

func (h *connectorHarness) auditRecords(t *testing.T) []*connectorsmodule.ConnectorCallRecord {
	t.Helper()
	records, err := h.store.Connectors().ListCallRecordsByRun(context.Background(), "WS", h.runID, connectorsmodule.ConnectorCallFilter{})
	if err != nil {
		t.Fatalf("ListByRun: %v", err)
	}
	return records
}

func TestConnectorDispatchAuthRefusals(t *testing.T) {
	h := newConnectorHarness(t)
	staleOwner := bearer(h.mintToken(t, time.Hour, func(claims *driverpkg.RunTokenClaims) {
		claims.LeaseID = "lease-stolen"
	}))
	unknownRun := bearer(h.mintToken(t, time.Hour, func(claims *driverpkg.RunTokenClaims) {
		claims.RunID = "run-unknown"
	}))
	cases := []struct {
		name     string
		headers  map[string]string
		wantHTTP int
		wantCode string
	}{
		{"missing run headers", nil, http.StatusUnauthorized, "unauthenticated"},
		{"unknown parent run", unknownRun, http.StatusNotFound, "not_found"},
		{"stale owner lease", staleOwner, http.StatusForbidden, "not_owner"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, decoded := h.do(t, opRequest{
				op: "connector-dispatch", body: h.dispatchBody("github.issues.comment"), headers: tc.headers,
			})
			if resp.StatusCode != tc.wantHTTP {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantHTTP)
			}
			if code := errorCode(t, decoded); code != tc.wantCode {
				t.Fatalf("error code = %q, want %q", code, tc.wantCode)
			}
		})
	}
	if len(h.provider.calls) != 0 {
		t.Fatalf("provider called %d times before authentication, want 0", len(h.provider.calls))
	}
	if records := h.auditRecords(t); len(records) != 0 {
		t.Fatalf("audit rows = %d before authentication, want 0", len(records))
	}
}

func TestConnectorDispatchHappyPath(t *testing.T) {
	h := newConnectorHarness(t)
	status, raw := h.doRaw(t, h.dispatchBody("github.issues.comment"), h.ownerHeaders(t))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}

	// CamelCase wire golden: exactly these keys, exactly these values.
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	keys := make([]string, 0, len(decoded))
	for k := range decoded {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if got, want := strings.Join(keys, ","), "body,callId,decision,status"; got != want {
		t.Fatalf("response keys = %q, want %q", got, want)
	}
	wantCallID := connectorsmodule.ConnectorCallID(h.runID, "github.issues.comment", 1)
	if decoded["callId"] != wantCallID || decoded["decision"] != "granted" || decoded["status"] != float64(200) {
		t.Fatalf("response = %v, want callId=%q decision=granted status=200", decoded, wantCallID)
	}
	body, _ := decoded["body"].(map[string]any)
	if body["commentId"] != "c-1" {
		t.Fatalf("body = %v, want commentId=c-1", decoded["body"])
	}

	// The provider saw the unsealed credential and the deterministic
	// idempotency key; the response never did.
	if len(h.provider.calls) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(h.provider.calls))
	}
	spec := h.provider.calls[0]
	if spec.Credential != connectorTestCredential {
		t.Fatalf("provider credential = %q, want sealed plaintext", spec.Credential)
	}
	if spec.IdempotencyKey != wantCallID {
		t.Fatalf("idempotency key = %q, want %q", spec.IdempotencyKey, wantCallID)
	}

	records := h.auditRecords(t)
	if len(records) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(records))
	}
	rec := records[0]
	if rec.Decision != connectorsmodule.ConnectorCallGranted || rec.BindingID != h.bindingID ||
		rec.ConnectorID != "gh-main" || rec.UpstreamStatus != 200 || rec.CallID != wantCallID {
		t.Fatalf("audit row = %+v, want granted/%s/gh-main/200/%s", rec, h.bindingID, wantCallID)
	}
}

func TestConnectorDispatchGrantDenied(t *testing.T) {
	h := newConnectorHarness(t)
	status, raw := h.doRaw(t, h.dispatchBody("github.branch.create"), h.ownerHeaders(t))
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", status, raw)
	}
	assertOpErrorCode(t, raw, "grant_denied", false)
	if len(h.provider.calls) != 0 {
		t.Fatalf("provider calls = %d on denied grant, want 0", len(h.provider.calls))
	}
	records := h.auditRecords(t)
	if len(records) != 1 || records[0].Decision != connectorsmodule.ConnectorCallDenied {
		t.Fatalf("audit rows = %+v, want exactly one denied row", records)
	}
}

func TestConnectorDispatchNoBindingDeniedWithoutAudit(t *testing.T) {
	// A run with no trigger lineage has no binding, hence no grants:
	// deny-by-default refuses before any dispatch or audit work.
	h := newTestHarness(t)
	dispatcher := unreachableConnectorDispatcher{} // never reached
	h.module.dispatcher = dispatcher
	resp, decoded := h.do(t, opRequest{
		op:      "connector-dispatch",
		headers: h.ownerHeaders(t),
		body: map[string]any{
			"connectorId": "gh-main", "action": "github.issues.comment",
			"resource": "repo:octocat/hello", "callSeq": 1,
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "grant_denied" {
		t.Fatalf("error code = %q, want grant_denied", code)
	}
	records, err := h.store.Connectors().ListCallRecordsByRun(context.Background(), "WS", h.runID, connectorsmodule.ConnectorCallFilter{})
	if err != nil {
		t.Fatalf("ListByRun: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("audit rows = %d for bindingless refusal, want 0", len(records))
	}
}

func TestConnectorDispatchPreconditionRequired(t *testing.T) {
	h := newConnectorHarness(t)
	body := h.dispatchBody("github.merge") // granted, but no expectedHeadSha supplied
	status, raw := h.doRaw(t, body, h.ownerHeaders(t))
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", status, raw)
	}
	assertOpErrorCode(t, raw, "precondition_required", false)
	if len(h.provider.calls) != 0 {
		t.Fatalf("provider calls = %d before precondition, want 0", len(h.provider.calls))
	}
	records := h.auditRecords(t)
	if len(records) != 1 || records[0].Decision != connectorsmodule.ConnectorCallPreconditionRequired {
		t.Fatalf("audit rows = %+v, want one precondition_required row", records)
	}
}

func TestConnectorDispatchPreconditionSatisfied(t *testing.T) {
	h := newConnectorHarness(t)
	body := h.dispatchBody("github.merge")
	body["preconditions"] = map[string]any{"expectedHeadSha": "abc123"}
	status, raw := h.doRaw(t, body, h.ownerHeaders(t))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	if len(h.provider.calls) != 1 || h.provider.calls[0].Preconditions.ExpectedHeadSha != "abc123" {
		t.Fatalf("provider calls = %+v, want one call carrying expectedHeadSha", h.provider.calls)
	}
}

func TestConnectorDispatchStaleSubject(t *testing.T) {
	h := newConnectorHarness(t)
	h.provider.result = providers.CallResult{Status: http.StatusConflict, Decision: connectorsmodule.ConnectorCallStaleSubject}
	h.provider.err = &providers.StaleSubject{
		Action: "github.merge", Resource: "repo:octocat/hello",
		Expected: "abc123", Reason: "head moved",
	}
	body := h.dispatchBody("github.merge")
	body["preconditions"] = map[string]any{"expectedHeadSha": "abc123"}
	status, raw := h.doRaw(t, body, h.ownerHeaders(t))
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", status, raw)
	}
	assertOpErrorCode(t, raw, "stale_subject", false)
	records := h.auditRecords(t)
	if len(records) != 1 || records[0].Decision != connectorsmodule.ConnectorCallStaleSubject {
		t.Fatalf("audit rows = %+v, want one stale_subject row", records)
	}
}

func TestConnectorDispatchRateLimited(t *testing.T) {
	h := newConnectorHarness(t)
	h.provider.result = providers.CallResult{Status: http.StatusTooManyRequests}
	h.provider.err = &providers.RateLimited{Action: "github.issues.comment", Status: http.StatusTooManyRequests}
	status, raw := h.doRaw(t, h.dispatchBody("github.issues.comment"), h.ownerHeaders(t))
	if status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (body %s)", status, raw)
	}
	assertOpErrorCode(t, raw, "rate_limited", true)
}

func TestConnectorDispatchUpstreamError(t *testing.T) {
	h := newConnectorHarness(t)
	h.provider.result = providers.CallResult{Status: http.StatusBadGateway}
	h.provider.err = &providers.UpstreamError{
		Action: "github.issues.comment", Class: providers.ClassServerError, Status: http.StatusBadGateway,
	}
	status, raw := h.doRaw(t, h.dispatchBody("github.issues.comment"), h.ownerHeaders(t))
	if status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body %s)", status, raw)
	}
	assertOpErrorCode(t, raw, "upstream_error", true)
}

func TestConnectorDispatchUnavailableWithoutDispatcher(t *testing.T) {
	h := newTestHarness(t) // Config without Dispatcher: egress fails closed
	resp, decoded := h.do(t, opRequest{
		op:      "connector-dispatch",
		headers: h.ownerHeaders(t),
		body:    map[string]any{"connectorId": "gh-main", "action": "github.issues.comment", "resource": "r:x"},
	})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "unavailable" {
		t.Fatalf("error code = %q, want unavailable", code)
	}
}

// TestConnectorDispatchNeverLeaksCredential drives granted, denied,
// precondition and upstream-error flows, then scans every HTTP response and
// every audit row for the plaintext credential.
func TestConnectorDispatchNeverLeaksCredential(t *testing.T) {
	h := newConnectorHarness(t)
	var responses [][]byte

	_, raw := h.doRaw(t, h.dispatchBody("github.issues.comment"), h.ownerHeaders(t))
	responses = append(responses, raw)
	_, raw = h.doRaw(t, h.dispatchBody("github.branch.create"), h.ownerHeaders(t))
	responses = append(responses, raw)
	_, raw = h.doRaw(t, h.dispatchBody("github.merge"), h.ownerHeaders(t))
	responses = append(responses, raw)

	// A provider rudely echoing the credential upstream must still come back
	// sanitized... but providers pre-sanitize; the stub asserts the handler
	// itself never adds the credential anywhere.
	h.provider.result = providers.CallResult{Status: http.StatusBadGateway}
	h.provider.err = &providers.UpstreamError{
		Action: "github.issues.comment", Class: providers.ClassServerError,
		Status: http.StatusBadGateway, Summary: "upstream exploded",
	}
	body := h.dispatchBody("github.issues.comment")
	body["callSeq"] = 2
	_, raw = h.doRaw(t, body, h.ownerHeaders(t))
	responses = append(responses, raw)

	for i, resp := range responses {
		if strings.Contains(string(resp), connectorTestCredential) {
			t.Fatalf("response %d leaks the plaintext credential: %s", i, resp)
		}
	}
	for _, rec := range h.auditRecords(t) {
		encoded, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("marshal audit row: %v", err)
		}
		if strings.Contains(string(encoded), connectorTestCredential) {
			t.Fatalf("audit row leaks the plaintext credential: %s", encoded)
		}
	}
}

// assertOpErrorCode decodes a structured error envelope from raw and checks
// code + retryable.
func assertOpErrorCode(t *testing.T, raw []byte, wantCode string, wantRetryable bool) {
	t.Helper()
	var decoded struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal error envelope: %v (body %s)", err, raw)
	}
	if decoded.Error.Code != wantCode {
		t.Fatalf("error code = %q, want %q (body %s)", decoded.Error.Code, wantCode, raw)
	}
	if decoded.Error.Retryable != wantRetryable {
		t.Fatalf("retryable = %v, want %v (body %s)", decoded.Error.Retryable, wantRetryable, raw)
	}
}
