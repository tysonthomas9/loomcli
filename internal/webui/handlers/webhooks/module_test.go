package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/domain"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	testWS     = "WS"
	testSecret = "hooksecret"
	testRoute  = "github.pull_request.opened"
)

func seedStoreWithoutConnector(t *testing.T, enabled bool) *memstore.Store {
	t.Helper()
	st := memstore.New()
	ctx := context.Background()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: testWS, DriverID: "github-pr-review", Name: "github-pr-review",
		Status: workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("seed driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: testWS, VersionID: "v1", DriverID: "github-pr-review", Version: 1,
		SourceDigest: "sha256:src", BundleDigest: "sha256:bundle",
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("seed version: %v", err)
	}
	if _, err := st.ApproveDriverVersionForTest(ctx, testWS, "github-pr-review", "v1"); err != nil {
		t.Fatalf("approve version: %v", err)
	}
	if _, err := st.ActivateDriverVersionForTest(ctx, testWS, "github-pr-review", "v1"); err != nil {
		t.Fatalf("activate version: %v", err)
	}
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: testWS, BindingID: "b1", Name: "pr-review", SourceKind: "github",
		RouteKey: testRoute, DriverID: "github-pr-review", DriverVersionID: "v1",
		Enabled: enabled,
	}); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	return st
}

func seedStore(t *testing.T, enabled bool) *memstore.Store {
	t.Helper()
	st := seedStoreWithoutConnector(t, enabled)
	if _, err := st.Connectors().CreateConnectorRecord(context.Background(), connectorsmodule.CreateConnectorMutation{
		WorkspaceKey: testWS, ConnectorID: "github-main", SourceKind: connectorsmodule.ConnectorSourceGitHub,
		InboundSecret: testSecret, Status: connectorsmodule.ConnectorStatusActive,
	}); err != nil {
		t.Fatalf("seed connector: %v", err)
	}
	return st
}

type testBindingQueries struct{ bindings store.TriggerBindingStore }

func (queries testBindingQueries) GetBinding(ctx context.Context, workspace, bindingID string) (*automation.Binding, error) {
	binding, err := queries.bindings.Get(ctx, workspace, bindingID)
	return binding, automationTestError(err)
}

func (queries testBindingQueries) ListBindings(ctx context.Context, workspace string, filter automation.BindingFilter) ([]*automation.Binding, error) {
	bindings, err := queries.bindings.List(ctx, workspace, store.TriggerBindingFilter{
		SourceKind: filter.SourceKind, RouteKey: filter.RouteKey, DriverID: filter.DriverID,
		TargetAgentServiceID: filter.TargetAgentServiceID, Enabled: filter.Enabled, Limit: filter.Limit,
	})
	return bindings, automationTestError(err)
}

// legacyAdmission is deliberately test-only. It lets the pre-Phase-3 router
// behavior suites drive the new named workflow while their fixtures still use
// memstore's legacy trigger dispatcher. Production handler files contain no
// corresponding fallback.
type legacyAdmission struct{ st store.Store }

func (adapter legacyAdmission) AdmitEvent(ctx context.Context, _ automation.EventAuthority, command automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
	if adapter.st == nil || adapter.st.TriggerRoutes() == nil {
		return nil, automation.ErrUnavailable
	}
	// Mirror Automation's centralized derived-admission boundary in this
	// deliberately legacy test adapter. Production always calls the real core;
	// this keeps signed HTTP regression tests on the same shared policy.
	if !automation.EligibleForAdmission(
		command.EventType, string(automation.EventOriginExternal), command.SourceKind,
		command.ActorRef, command.SourceEventID,
	) {
		return nil, automation.ErrInvalid
	}
	result, err := adapter.st.TriggerRoutes().DispatchTriggerRouteV2(ctx, command.WorkspaceKey, command.RouteKey, store.TriggerRouteDispatch{
		IdempotencyKey: command.SourceKind + ":" + command.SourceEventID,
		SourceEventID:  command.SourceEventID, EventType: command.EventType,
		SubjectRef: command.SubjectRef, ActorRef: command.ActorRef,
		SignatureStatus: "verified", RawPayloadRef: command.RawPayloadRef,
		RawPayloadDigest: command.RawPayloadDigest, Payload: command.Payload,
		SubjectAttrs: command.SubjectAttrs,
	})
	if err != nil {
		return nil, automationTestError(err)
	}
	deliveries := make([]*automation.Delivery, 0, len(result.Deliveries))
	for _, delivery := range result.Deliveries {
		deliveries = append(deliveries, &automation.Delivery{
			DeliveryID: delivery.DeliveryID, TriggerBindingID: delivery.BindingID,
			DriverRunID: delivery.RunID, Status: delivery.Status,
			RejectionReason: delivery.RejectionReason,
		})
	}
	return &automation.AdmissionResult{Deliveries: deliveries}, nil
}

type legacyQueries struct{ st store.Store }

func (adapter legacyQueries) GetEvent(ctx context.Context, workspace, eventID string) (*automation.Event, error) {
	event, err := adapter.st.TriggerEvents().Get(ctx, workspace, eventID)
	return event, automationTestError(err)
}

func (adapter legacyQueries) ListEvents(ctx context.Context, workspace string, filter automation.EventFilter) ([]*automation.Event, error) {
	events, err := adapter.st.TriggerEvents().List(ctx, workspace, store.TriggerEventFilter{
		SourceKind: filter.SourceKind, TriggerBindingID: filter.BindingID, Limit: filter.Limit,
	})
	return events, automationTestError(err)
}

func (adapter legacyQueries) GetDelivery(ctx context.Context, workspace, deliveryID string) (*automation.Delivery, error) {
	delivery, err := adapter.st.TriggerDeliveries().Get(ctx, workspace, deliveryID)
	return delivery, automationTestError(err)
}

func (adapter legacyQueries) ListDeliveries(ctx context.Context, workspace string, filter automation.DeliveryFilter) ([]*automation.Delivery, error) {
	deliveries, err := adapter.st.TriggerDeliveries().List(ctx, workspace, store.TriggerDeliveryFilter{
		TriggerEventID: filter.EventID, TriggerBindingID: filter.BindingID,
		Status: filter.Status, Limit: filter.Limit,
	})
	return deliveries, automationTestError(err)
}

func automationTestError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return errors.Join(automation.ErrNotFound, err)
	case errors.Is(err, domain.ErrInvalid):
		return errors.Join(automation.ErrInvalid, err)
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrAlreadyExists):
		return errors.Join(automation.ErrConflict, err)
	default:
		return err
	}
}

type testAuthorityProvider struct{}

func (testAuthorityProvider) AuthorityForVerifiedWebhook(context.Context, webhookingestion.AuthorityRequest) (authority.WebhookAuthority, error) {
	return authority.WebhookAuthority{}, nil
}

func newServer(st store.Store) *http.ServeMux {
	mux := http.NewServeMux()
	resolver, ok := st.Awaits().(store.AtomicAwaitStore)
	if !ok {
		panic("webhook test store lacks atomic await resolution")
	}
	workflow, err := webhookingestion.New(
		NewVerifier(VerifierConfig{
			Bindings: testBindingQueries{bindings: st.TriggerBindings()}, Connectors: st.Connectors(),
		}),
		testAuthorityProvider{}, legacyAdmission{st: st},
	)
	if err != nil {
		panic(err)
	}
	New(Config{
		Workflow: workflow, Automation: legacyQueries{st: st},
		Awaits: trigger.NewAwaitMatcherWithResolver(st.Awaits(), st.DriverRuns(), resolver),
	}).Register(mux)
	return mux
}

func signedRequest(name, delivery string, body []byte) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+testWS+"/webhooks/"+name, bytes.NewReader(body))
	r.Header.Set(githubEventHeader, "pull_request")
	r.Header.Set(githubDeliveryHeader, delivery)
	r.Header.Set(githubSignatureHeader, githubSignature(testSecret, body))
	return r
}

var prOpenedBody = []byte(`{"action":"opened","pull_request":{"number":7},"repository":{"full_name":"acme/widgets"},"sender":{"login":"octocat"}}`)

// webhookResponse decodes the 202 body on the BREAKING router-v2 wire:
// deliveries[] only, no top-level driver_run_id / driver_run.
type webhookResponse struct {
	Status         string                       `json:"status"`
	RouteKey       string                       `json:"route_key"`
	IdempotencyKey string                       `json:"idempotency_key"`
	Deliveries     []store.TriggerRouteDelivery `json:"deliveries"`
}

func decodeWebhookResponse(t *testing.T, rr *httptest.ResponseRecorder) webhookResponse {
	t.Helper()
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp webhookResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func TestReceiveWebhookDispatchesDriverRun(t *testing.T) {
	st := seedStore(t, true)
	mux := newServer(st)
	ctx := context.Background()

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, signedRequest("github", "delivery-1", prOpenedBody))

	resp := decodeWebhookResponse(t, rr)
	if resp.Status != "accepted" || resp.RouteKey != testRoute || resp.IdempotencyKey != "github:delivery-1" {
		t.Fatalf("unexpected response envelope: %+v", resp)
	}
	// Single-binding lane on the new wire: exactly one delivery leg. The old
	// top-level driver_run_id/driver_run keys are gone (BREAKING fan-out wire,
	// locked decision overriding the pre-fan-out single-run response).
	if len(resp.Deliveries) != 1 {
		t.Fatalf("want 1 delivery leg, got %d: %+v", len(resp.Deliveries), resp.Deliveries)
	}
	leg := resp.Deliveries[0]
	if leg.BindingID != "b1" || leg.RunID == "" || leg.DeliveryID == "" || leg.Status != automation.DeliveryDispatched {
		t.Fatalf("unexpected delivery leg: %+v", leg)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw response: %v", err)
	}
	for _, key := range []string{"driver_run_id", "driver_run"} {
		if _, ok := raw[key]; ok {
			t.Errorf("response still carries removed top-level key %q", key)
		}
	}

	// TriggerEvent persisted with verified signature + digest.
	events, _ := st.TriggerEvents().List(ctx, testWS, store.TriggerEventFilter{})
	if len(events) != 1 {
		t.Fatalf("want 1 trigger event, got %d", len(events))
	}
	ev := events[0]
	if ev.SignatureStatus != "verified" {
		t.Errorf("signature_status = %q, want verified", ev.SignatureStatus)
	}
	if ev.SourceEventID != "delivery-1" {
		t.Errorf("source_event_id = %q, want delivery-1", ev.SourceEventID)
	}
	if ev.RawPayloadDigest == "" {
		t.Error("raw_payload_digest is empty")
	}

	// TriggerDelivery linked to the run.
	deliveries, _ := st.TriggerDeliveries().List(ctx, testWS, store.TriggerDeliveryFilter{})
	if len(deliveries) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(deliveries))
	}
	if deliveries[0].DriverRunID != leg.RunID || deliveries[0].TriggerEventID != ev.EventID {
		t.Errorf("delivery not linked: %+v", deliveries[0])
	}
	if deliveries[0].DeliveryID != leg.DeliveryID {
		t.Errorf("persisted delivery id %q != response leg %q", deliveries[0].DeliveryID, leg.DeliveryID)
	}

	// Queued DriverRun carrying the original payload.
	runs, _ := st.DriverRuns().List(ctx, testWS, store.DriverRunFilter{Status: domain.DriverRunQueued})
	if len(runs) != 1 {
		t.Fatalf("want 1 queued run, got %d", len(runs))
	}
	var payload struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(runs[0].Payload, &payload); err != nil || payload.Action != "opened" {
		t.Errorf("run payload = %s (err %v)", runs[0].Payload, err)
	}
	if runs[0].DriverVersionID != "v1" {
		t.Errorf("run pinned version = %q, want v1", runs[0].DriverVersionID)
	}
}

func TestReceiveWebhookDuplicateDeliveryIsIdempotent(t *testing.T) {
	st := seedStore(t, true)
	mux := newServer(st)
	ctx := context.Background()

	first := httptest.NewRecorder()
	mux.ServeHTTP(first, signedRequest("github", "delivery-dup", prOpenedBody))
	firstResp := decodeWebhookResponse(t, first)
	second := httptest.NewRecorder()
	mux.ServeHTTP(second, signedRequest("github", "delivery-dup", prOpenedBody))
	secondResp := decodeWebhookResponse(t, second)

	// Redelivery of the same X-GitHub-Delivery must surface the same run and
	// delivery ids — the dispatch path dedups per leg.
	if len(firstResp.Deliveries) != 1 || len(secondResp.Deliveries) != 1 {
		t.Fatalf("want 1 delivery leg each, got %d and %d", len(firstResp.Deliveries), len(secondResp.Deliveries))
	}
	if firstResp.Deliveries[0].RunID != secondResp.Deliveries[0].RunID {
		t.Errorf("redelivery changed run id: %q != %q", firstResp.Deliveries[0].RunID, secondResp.Deliveries[0].RunID)
	}
	if firstResp.Deliveries[0].DeliveryID != secondResp.Deliveries[0].DeliveryID {
		t.Errorf("redelivery changed delivery id: %q != %q", firstResp.Deliveries[0].DeliveryID, secondResp.Deliveries[0].DeliveryID)
	}

	events, _ := st.TriggerEvents().List(ctx, testWS, store.TriggerEventFilter{})
	deliveries, _ := st.TriggerDeliveries().List(ctx, testWS, store.TriggerDeliveryFilter{})
	runs, _ := st.DriverRuns().List(ctx, testWS, store.DriverRunFilter{})
	if len(events) != 1 || len(deliveries) != 1 || len(runs) != 1 {
		t.Fatalf("duplicate delivery created extra state: events=%d deliveries=%d runs=%d",
			len(events), len(deliveries), len(runs))
	}
}

func TestReceiveWebhookFanOutResponseListsAllDeliveries(t *testing.T) {
	st := seedStore(t, true)
	ctx := context.Background()
	// Pattern-only binding (no RouteKey) matching the ingress route: fans out
	// alongside the exact b1 owner. Signature verification stays keyed to b1's
	// secret — pattern bindings carry none (locked interim decision).
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: testWS, BindingID: "b2-pattern", Name: "pr-audit", SourceKind: "github",
		EventTypePatterns: []string{"github.pull_request.*"},
		DriverID:          "github-pr-review", DriverVersionID: "v1", Enabled: true,
	}); err != nil {
		t.Fatalf("seed pattern binding: %v", err)
	}
	mux := newServer(st)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, signedRequest("github", "delivery-fan", prOpenedBody))
	resp := decodeWebhookResponse(t, rr)

	// Exact RouteKey owner first, then pattern matches in binding-id order.
	if len(resp.Deliveries) != 2 {
		t.Fatalf("want 2 delivery legs, got %d: %+v", len(resp.Deliveries), resp.Deliveries)
	}
	if resp.Deliveries[0].BindingID != "b1" || resp.Deliveries[1].BindingID != "b2-pattern" {
		t.Fatalf("unexpected leg order: %+v", resp.Deliveries)
	}
	for i, leg := range resp.Deliveries {
		if leg.RunID == "" || leg.DeliveryID == "" || leg.Status != automation.DeliveryDispatched {
			t.Errorf("leg %d incomplete: %+v", i, leg)
		}
	}
	if resp.Deliveries[0].RunID == resp.Deliveries[1].RunID {
		t.Errorf("legs share run id %q", resp.Deliveries[0].RunID)
	}

	// One event, one delivery + run per leg.
	events, _ := st.TriggerEvents().List(ctx, testWS, store.TriggerEventFilter{})
	deliveries, _ := st.TriggerDeliveries().List(ctx, testWS, store.TriggerDeliveryFilter{})
	runs, _ := st.DriverRuns().List(ctx, testWS, store.DriverRunFilter{})
	if len(events) != 1 || len(deliveries) != 2 || len(runs) != 2 {
		t.Fatalf("fan-out state: events=%d deliveries=%d runs=%d, want 1/2/2",
			len(events), len(deliveries), len(runs))
	}

	// Redelivery returns the same legs with stable ids and creates no state.
	again := httptest.NewRecorder()
	mux.ServeHTTP(again, signedRequest("github", "delivery-fan", prOpenedBody))
	replay := decodeWebhookResponse(t, again)
	if len(replay.Deliveries) != 2 {
		t.Fatalf("replay want 2 legs, got %d", len(replay.Deliveries))
	}
	for i := range replay.Deliveries {
		if replay.Deliveries[i].RunID != resp.Deliveries[i].RunID {
			t.Errorf("replay leg %d run id %q != %q", i, replay.Deliveries[i].RunID, resp.Deliveries[i].RunID)
		}
	}
	runs, _ = st.DriverRuns().List(ctx, testWS, store.DriverRunFilter{})
	if len(runs) != 2 {
		t.Fatalf("replay created runs: got %d, want 2", len(runs))
	}
}

// stubDispatcher is a canned TriggerRouteDispatcher that records its input so
// handler tests can pin the dispatch wire (incl. SubjectAttrs plumbing) and
// surface result shapes the memstore never produces, e.g. a rejected leg from
// a forbid concurrency policy (admission lives in fleet-db).
type stubDispatcher struct {
	gotWS    string
	gotRoute string
	gotIn    store.TriggerRouteDispatch
	result   *store.TriggerRouteDispatchResult
}

func (s *stubDispatcher) DispatchTriggerRoute(ctx context.Context, ws, routeKey string, in store.TriggerRouteDispatch) (*domain.DriverRun, error) {
	result, err := s.DispatchTriggerRouteV2(ctx, ws, routeKey, in)
	if err != nil {
		return nil, err
	}
	return result.PrimaryRun, nil
}

func (s *stubDispatcher) DispatchTriggerRouteV2(_ context.Context, ws, routeKey string, in store.TriggerRouteDispatch) (*store.TriggerRouteDispatchResult, error) {
	s.gotWS, s.gotRoute, s.gotIn = ws, routeKey, in
	return s.result, nil
}

// dispatcherOverrideStore swaps the dispatcher while keeping memstore's
// binding/secret lookups so authorizeWebhook still runs for real.
type dispatcherOverrideStore struct {
	store.Store
	dispatcher store.TriggerRouteDispatcher
}

func (s dispatcherOverrideStore) TriggerRoutes() store.TriggerRouteDispatcher { return s.dispatcher }

func TestReceiveWebhookSurfacesRejectedLegAndPlumbsSubjectAttrs(t *testing.T) {
	stub := &stubDispatcher{result: &store.TriggerRouteDispatchResult{
		Deliveries: []store.TriggerRouteDelivery{
			{DeliveryID: "delivery-e1-b1", BindingID: "b1", RunID: "run-aaa", Status: automation.DeliveryDispatched},
			{DeliveryID: "delivery-e1-b3", BindingID: "b3-forbid", Status: automation.DeliveryRejected,
				RejectionReason: "concurrency policy forbid: active run for subject acme/widgets#7"},
		},
	}}
	mux := newServer(dispatcherOverrideStore{Store: seedStore(t, true), dispatcher: stub})

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, signedRequest("github", "delivery-rej", prOpenedBody))
	resp := decodeWebhookResponse(t, rr)

	if len(resp.Deliveries) != 2 {
		t.Fatalf("want 2 delivery legs, got %d: %+v", len(resp.Deliveries), resp.Deliveries)
	}
	rejected := resp.Deliveries[1]
	if rejected.Status != automation.DeliveryRejected || rejected.RejectionReason == "" || rejected.RunID != "" {
		t.Errorf("rejected leg not surfaced: %+v", rejected)
	}

	// Dispatch input wire: SubjectAttrs from the adapter (C15) reach the
	// dispatcher alongside the normalized routing fields.
	if stub.gotWS != testWS || stub.gotRoute != testRoute {
		t.Errorf("dispatch routing = (%q, %q), want (%q, %q)", stub.gotWS, stub.gotRoute, testWS, testRoute)
	}
	if stub.gotIn.IdempotencyKey != "github:delivery-rej" || stub.gotIn.SignatureStatus != "verified" {
		t.Errorf("dispatch input envelope: %+v", stub.gotIn)
	}
	wantAttrs := map[string]string{"repo": "acme/widgets", "pr_number": "7"}
	for key, want := range wantAttrs {
		if got := stub.gotIn.SubjectAttrs[key]; got != want {
			t.Errorf("SubjectAttrs[%q] = %q, want %q (all: %v)", key, got, want, stub.gotIn.SubjectAttrs)
		}
	}
}

func TestReceiveWebhookRejectsBadSignature(t *testing.T) {
	st := seedStore(t, true)
	mux := newServer(st)
	ctx := context.Background()

	r := signedRequest("github", "delivery-x", prOpenedBody)
	r.Header.Set(githubSignatureHeader, "sha256=deadbeef") // wrong
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, r)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body %s", rr.Code, rr.Body.String())
	}
	events, _ := st.TriggerEvents().List(ctx, testWS, store.TriggerEventFilter{})
	runs, _ := st.DriverRuns().List(ctx, testWS, store.DriverRunFilter{})
	if len(events) != 0 || len(runs) != 0 {
		t.Fatalf("unverified request created state: events=%d runs=%d", len(events), len(runs))
	}
}

func TestReceiveWebhookDisabledBinding(t *testing.T) {
	mux := newServer(seedStore(t, false))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, signedRequest("github", "d", prOpenedBody))
	if rr.Code != http.StatusUnauthorized || rr.Body.String() != uniform401Body {
		t.Fatalf("disabled binding denial = %d %q, want uniform 401", rr.Code, rr.Body.String())
	}
}

func TestReceiveWebhookUnknownAdapter(t *testing.T) {
	mux := newServer(seedStore(t, true))
	r := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+testWS+"/webhooks/gitlab", bytes.NewReader(prOpenedBody))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, r)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unknown adapter", rr.Code)
	}
}

func TestReceiveWebhookNoBindingForRoute(t *testing.T) {
	mux := newServer(seedStore(t, true))
	// A closed action has no binding, but route presence is not exposed through
	// a distinct response: missing, disabled, and bad-signature requests share
	// the same denial.
	body := []byte(`{"action":"closed","repository":{"full_name":"acme/widgets"}}`)
	r := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+testWS+"/webhooks/github", bytes.NewReader(body))
	r.Header.Set(githubEventHeader, "pull_request")
	r.Header.Set(githubDeliveryHeader, "d-closed")
	r.Header.Set(githubSignatureHeader, githubSignature(testSecret, body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, r)
	if rr.Code != http.StatusUnauthorized || rr.Body.String() != uniform401Body {
		t.Fatalf("unbound route denial = %d %q, want uniform 401", rr.Code, rr.Body.String())
	}
}

func TestListTriggerEventsAndDeliveries(t *testing.T) {
	st := seedStore(t, true)
	mux := newServer(st)
	mux.ServeHTTP(httptest.NewRecorder(), signedRequest("github", "delivery-list", prOpenedBody))

	evRec := httptest.NewRecorder()
	mux.ServeHTTP(evRec, httptest.NewRequest(http.MethodGet, "/api/workspaces/"+testWS+"/trigger-events", nil))
	if evRec.Code != http.StatusOK {
		t.Fatalf("events list status = %d", evRec.Code)
	}
	var evResp struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(evRec.Body.Bytes(), &evResp)
	if evResp.Count != 1 {
		t.Errorf("events count = %d, want 1", evResp.Count)
	}

	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, httptest.NewRequest(http.MethodGet, "/api/workspaces/"+testWS+"/trigger-deliveries", nil))
	if delRec.Code != http.StatusOK {
		t.Fatalf("deliveries list status = %d", delRec.Code)
	}
	var delResp struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(delRec.Body.Bytes(), &delResp)
	if delResp.Count != 1 {
		t.Errorf("deliveries count = %d, want 1", delResp.Count)
	}
}
