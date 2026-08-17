package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
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
	if _, err := st.Drivers().Create(ctx, workflowcatalog.DriverCreate{
		WorkspaceKey: testWS, DriverID: "github-pr-review", Name: "github-pr-review",
		Status: workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("seed driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, workflowcatalog.DriverVersionCreate{
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
	if _, err := st.TriggerBindings().Create(ctx, automation.TriggerBindingCreate{
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

type testBindingQueries struct {
	bindings automation.TriggerBindingStore
}

func (queries testBindingQueries) GetBinding(ctx context.Context, workspace, bindingID string) (*automation.Binding, error) {
	binding, err := queries.bindings.Get(ctx, workspace, bindingID)
	return binding, automationTestError(err)
}

func (queries testBindingQueries) ListBindings(ctx context.Context, workspace string, filter automation.BindingFilter) ([]*automation.Binding, error) {
	bindings, err := queries.bindings.List(ctx, workspace, automation.TriggerBindingFilter(filter))
	return bindings, automationTestError(err)
}

// testAdmission is a narrow fake of Automation's public admission port. It
// deliberately does not reproduce matching, reservation, persistence,
// idempotency, or dispatch; those behaviors belong to Automation's tests.
type testAdmission struct {
	commands []automation.WebhookEvent
	result   *automation.AdmissionResult
	err      error
}

func (adapter *testAdmission) AdmitWebhookEvent(_ context.Context, _ authority.WebhookAuthority, command automation.WebhookEvent) (*automation.AdmissionResult, error) {
	adapter.commands = append(adapter.commands, command)
	if adapter.result == nil && adapter.err == nil {
		return &automation.AdmissionResult{Deliveries: []*automation.Delivery{{
			DeliveryID: "delivery-b1", TriggerBindingID: "b1",
			DriverRunID: "run-b1", Status: automation.DeliveryDispatched,
		}}}, nil
	}
	return adapter.result, adapter.err
}

type testAutomationQueries struct {
	events     []*automation.Event
	deliveries []*automation.Delivery
}

func (queries testAutomationQueries) GetEvent(_ context.Context, _, eventID string) (*automation.Event, error) {
	for _, event := range queries.events {
		if event != nil && event.EventID == eventID {
			return event, nil
		}
	}
	return nil, automation.ErrNotFound
}

func (queries testAutomationQueries) ListEvents(context.Context, string, automation.EventFilter) ([]*automation.Event, error) {
	return queries.events, nil
}

func (queries testAutomationQueries) GetDelivery(_ context.Context, _, deliveryID string) (*automation.Delivery, error) {
	for _, delivery := range queries.deliveries {
		if delivery != nil && delivery.DeliveryID == deliveryID {
			return delivery, nil
		}
	}
	return nil, automation.ErrNotFound
}

func (queries testAutomationQueries) ListDeliveries(context.Context, string, automation.DeliveryFilter) ([]*automation.Delivery, error) {
	return queries.deliveries, nil
}

func automationTestError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, persistence.ErrNotFound):
		return errors.Join(automation.ErrNotFound, err)
	case errors.Is(err, persistence.ErrInvalid):
		return errors.Join(automation.ErrInvalid, err)
	case errors.Is(err, persistence.ErrConflict), errors.Is(err, persistence.ErrAlreadyExists):
		return errors.Join(automation.ErrConflict, err)
	default:
		return err
	}
}

type testAuthorityProvider struct{}

func (testAuthorityProvider) AuthorityForVerifiedWebhook(context.Context, webhookingestion.AuthorityRequest) (authority.WebhookAuthority, error) {
	return authority.WebhookAuthority{}, nil
}

type webhookFixture interface {
	Connectors() connectorsmodule.ManagementStore
	TriggerBindings() automation.TriggerBindingStore
	Awaits() execution.AwaitStore
	DriverRuns() execution.DriverRunStore
}

func newServer(st webhookFixture) *http.ServeMux {
	return newServerWithPorts(st, &testAdmission{}, testAutomationQueries{})
}

func newServerWithPorts(st webhookFixture, admission automation.WebhookEventAdmission, queries AutomationQueries) *http.ServeMux {
	mux := http.NewServeMux()
	resolver, ok := st.Awaits().(execution.AtomicAwaitStore)
	if !ok {
		panic("webhook test store lacks atomic await resolution")
	}
	workflow, err := webhookingestion.New(
		NewVerifier(VerifierConfig{
			Bindings: testBindingQueries{bindings: st.TriggerBindings()}, Connectors: st.Connectors(),
		}),
		testAuthorityProvider{}, admission,
	)
	if err != nil {
		panic(err)
	}
	New(Config{
		Workflow: workflow, Automation: queries,
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
	Status         string             `json:"status"`
	RouteKey       string             `json:"route_key"`
	IdempotencyKey string             `json:"idempotency_key"`
	Deliveries     []deliveryResponse `json:"deliveries"`
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
	admission := &testAdmission{}
	mux := newServerWithPorts(st, admission, testAutomationQueries{})

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

	if len(admission.commands) != 1 {
		t.Fatalf("admission calls = %d, want 1", len(admission.commands))
	}
	command := admission.commands[0]
	if command.WorkspaceKey != testWS || command.RouteKey != testRoute || command.SourceEventID != "delivery-1" {
		t.Fatalf("admission command routing = %+v", command)
	}
	if command.RawPayloadDigest == "" {
		t.Error("admission command raw_payload_digest is empty")
	}
	var payload struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(command.Payload, &payload); err != nil || payload.Action != "opened" {
		t.Errorf("admission payload = %s (err %v)", command.Payload, err)
	}
	wantAttrs := map[string]string{"repo": "acme/widgets", "pr_number": "7"}
	for key, want := range wantAttrs {
		if got := command.SubjectAttrs[key]; got != want {
			t.Errorf("SubjectAttrs[%q] = %q, want %q", key, got, want)
		}
	}
}

func TestReceiveWebhookRedeliveryUsesStableAdmissionIdentity(t *testing.T) {
	st := seedStore(t, true)
	admission := &testAdmission{}
	mux := newServerWithPorts(st, admission, testAutomationQueries{})

	first := httptest.NewRecorder()
	mux.ServeHTTP(first, signedRequest("github", "delivery-dup", prOpenedBody))
	firstResp := decodeWebhookResponse(t, first)
	second := httptest.NewRecorder()
	mux.ServeHTTP(second, signedRequest("github", "delivery-dup", prOpenedBody))
	secondResp := decodeWebhookResponse(t, second)

	// Webhook Ingestion must pass the same source identity on redelivery;
	// Automation owns durable idempotency and stable delivery/run ids.
	if len(firstResp.Deliveries) != 1 || len(secondResp.Deliveries) != 1 {
		t.Fatalf("want 1 delivery leg each, got %d and %d", len(firstResp.Deliveries), len(secondResp.Deliveries))
	}
	if firstResp.Deliveries[0].RunID != secondResp.Deliveries[0].RunID {
		t.Errorf("redelivery changed run id: %q != %q", firstResp.Deliveries[0].RunID, secondResp.Deliveries[0].RunID)
	}
	if firstResp.Deliveries[0].DeliveryID != secondResp.Deliveries[0].DeliveryID {
		t.Errorf("redelivery changed delivery id: %q != %q", firstResp.Deliveries[0].DeliveryID, secondResp.Deliveries[0].DeliveryID)
	}

	if len(admission.commands) != 2 {
		t.Fatalf("admission calls = %d, want 2", len(admission.commands))
	}
	if firstID, secondID := admission.commands[0].SourceEventID, admission.commands[1].SourceEventID; firstID != secondID || firstID != "delivery-dup" {
		t.Fatalf("redelivery source ids = %q, %q", firstID, secondID)
	}
}

func TestReceiveWebhookFanOutResponseListsAllDeliveries(t *testing.T) {
	st := seedStore(t, true)
	admission := &testAdmission{result: &automation.AdmissionResult{Deliveries: []*automation.Delivery{
		{DeliveryID: "delivery-b1", TriggerBindingID: "b1", DriverRunID: "run-b1", Status: automation.DeliveryDispatched},
		{DeliveryID: "delivery-b2", TriggerBindingID: "b2-pattern", DriverRunID: "run-b2", Status: automation.DeliveryDispatched},
	}}}
	mux := newServerWithPorts(st, admission, testAutomationQueries{})

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

	// Redelivery returns the same legs because Automation's public result is
	// rendered without transport-side interpretation.
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
}

func TestReceiveWebhookSurfacesRejectedLegAndPlumbsSubjectAttrs(t *testing.T) {
	admission := &testAdmission{result: &automation.AdmissionResult{
		Deliveries: []*automation.Delivery{
			{DeliveryID: "delivery-e1-b1", TriggerBindingID: "b1", DriverRunID: "run-aaa", Status: automation.DeliveryDispatched},
			{DeliveryID: "delivery-e1-b3", TriggerBindingID: "b3-forbid", Status: automation.DeliveryRejected,
				RejectionReason: "concurrency policy forbid: active run for subject acme/widgets#7"},
		},
	}}
	mux := newServerWithPorts(seedStore(t, true), admission, testAutomationQueries{})

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

	if len(admission.commands) != 1 {
		t.Fatalf("admission calls = %d, want 1", len(admission.commands))
	}
	command := admission.commands[0]
	wantAttrs := map[string]string{"repo": "acme/widgets", "pr_number": "7"}
	for key, want := range wantAttrs {
		if got := command.SubjectAttrs[key]; got != want {
			t.Errorf("SubjectAttrs[%q] = %q, want %q (all: %v)", key, got, want, command.SubjectAttrs)
		}
	}
}

func TestReceiveWebhookRejectsBadSignature(t *testing.T) {
	st := seedStore(t, true)
	admission := &testAdmission{}
	mux := newServerWithPorts(st, admission, testAutomationQueries{})

	r := signedRequest("github", "delivery-x", prOpenedBody)
	r.Header.Set(githubSignatureHeader, "sha256=deadbeef") // wrong
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, r)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body %s", rr.Code, rr.Body.String())
	}
	if len(admission.commands) != 0 {
		t.Fatalf("unverified request reached admission %d times", len(admission.commands))
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
	queries := testAutomationQueries{
		events: []*automation.Event{{WorkspaceKey: testWS, EventID: "event-list", SourceKind: "github"}},
		deliveries: []*automation.Delivery{{
			WorkspaceKey: testWS, DeliveryID: "delivery-list", TriggerEventID: "event-list",
			TriggerBindingID: "b1", Status: automation.DeliveryDispatched,
		}},
	}
	mux := newServerWithPorts(st, &testAdmission{}, queries)

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
