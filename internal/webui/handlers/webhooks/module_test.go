package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	testWS     = "WS"
	testSecret = "hooksecret"
	testRoute  = "github.pull_request.opened"
)

func seedStore(t *testing.T, enabled bool) *memstore.Store {
	t.Helper()
	st := memstore.New()
	ctx := context.Background()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: testWS, DriverID: "github-pr-review", Name: "github-pr-review",
		Status: domain.DriverStatusActive, ActiveVersionID: "v1",
	}); err != nil {
		t.Fatalf("seed driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: testWS, VersionID: "v1", DriverID: "github-pr-review", Version: 1,
		SourceDigest: "sha256:src", BundleDigest: "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("seed version: %v", err)
	}
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: testWS, BindingID: "b1", Name: "pr-review", SourceKind: "github",
		RouteKey: testRoute, DriverID: "github-pr-review", DriverVersionID: "v1",
		WebhookSecret: testSecret, Enabled: enabled,
	}); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	return st
}

func newServer(st store.Store) *http.ServeMux {
	mux := http.NewServeMux()
	NewModule(st).Register(mux)
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

func TestReceiveWebhookDispatchesDriverRun(t *testing.T) {
	st := seedStore(t, true)
	mux := newServer(st)
	ctx := context.Background()

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, signedRequest("github", "delivery-1", prOpenedBody))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		RouteKey    string `json:"route_key"`
		DriverRunID string `json:"driver_run_id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.RouteKey != testRoute || resp.DriverRunID == "" {
		t.Fatalf("unexpected response: %+v", resp)
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
	if deliveries[0].DriverRunID != resp.DriverRunID || deliveries[0].TriggerEventID != ev.EventID {
		t.Errorf("delivery not linked: %+v", deliveries[0])
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

func TestBindingSecretRedactedButResolvable(t *testing.T) {
	st := seedStore(t, true)
	ctx := context.Background()
	got, err := st.TriggerBindings().GetByRouteKey(ctx, testWS, testRoute)
	if err != nil {
		t.Fatalf("GetByRouteKey: %v", err)
	}
	if got.WebhookSecret != "" {
		t.Errorf("GetByRouteKey leaked webhook_secret = %q, want redacted", got.WebhookSecret)
	}
	secret, err := st.TriggerBindings().ResolveWebhookSecret(ctx, testWS, got.BindingID)
	if err != nil || secret != testSecret {
		t.Fatalf("ResolveWebhookSecret = %q err=%v, want %q", secret, err, testSecret)
	}
}

func TestReceiveWebhookDuplicateDeliveryIsIdempotent(t *testing.T) {
	st := seedStore(t, true)
	mux := newServer(st)
	ctx := context.Background()

	first := httptest.NewRecorder()
	mux.ServeHTTP(first, signedRequest("github", "delivery-dup", prOpenedBody))
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d", first.Code)
	}
	second := httptest.NewRecorder()
	mux.ServeHTTP(second, signedRequest("github", "delivery-dup", prOpenedBody))
	if second.Code != http.StatusAccepted {
		t.Fatalf("second status = %d, body %s", second.Code, second.Body.String())
	}

	events, _ := st.TriggerEvents().List(ctx, testWS, store.TriggerEventFilter{})
	deliveries, _ := st.TriggerDeliveries().List(ctx, testWS, store.TriggerDeliveryFilter{})
	runs, _ := st.DriverRuns().List(ctx, testWS, store.DriverRunFilter{})
	if len(events) != 1 || len(deliveries) != 1 || len(runs) != 1 {
		t.Fatalf("duplicate delivery created extra state: events=%d deliveries=%d runs=%d",
			len(events), len(deliveries), len(runs))
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
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
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
	// closed action has no binding → 404, and we never reach verification.
	body := []byte(`{"action":"closed","repository":{"full_name":"acme/widgets"}}`)
	r := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+testWS+"/webhooks/github", bytes.NewReader(body))
	r.Header.Set(githubEventHeader, "pull_request")
	r.Header.Set(githubDeliveryHeader, "d-closed")
	r.Header.Set(githubSignatureHeader, githubSignature(testSecret, body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, r)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unbound route", rr.Code)
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
