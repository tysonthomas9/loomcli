// Router v2 end-to-end verification suite (chunk C18): drives the full
// loomcli router stack — HTTP webhook ingest, both pattern engines, fan-out,
// concurrency admission, the retry sweeper and the cron scheduler — against
// the real httptest mux + memstore wiring, asserting the same scenarios
// fleet-db pins API-side (platform_test.go supersede/admission,
// platform_retry_test.go due-index, platform_provenance_test.go origin).
//
// The binary-stack Tier-0 e2e (embedded fleet-db + loom serve processes)
// lives in webhooks_e2e_test.go behind the e2e build tag; this suite runs in
// the normal `go test` gate.
package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

const (
	routerE2EWS     = "ROUTER-E2E"
	routerE2ESecret = "router-e2e-secret"
)

// routerE2EStore seeds a memstore with the driver + version every binding in
// this suite targets.
func routerE2EStore(t *testing.T) *memstore.Store {
	t.Helper()
	st := memstore.New()
	ctx := context.Background()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: routerE2EWS, DriverID: "pr-review", Name: "pr-review",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("seed driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: routerE2EWS, VersionID: "v1", DriverID: "pr-review", Version: 1,
		SourceDigest: "sha256:src", BundleDigest: "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("seed driver version: %v", err)
	}
	return st
}

// routerE2EBinding creates a binding with the suite's shared target defaults
// applied; callers set identity, routing and policy fields.
func routerE2EBinding(t *testing.T, st *memstore.Store, in store.TriggerBindingCreate) {
	t.Helper()
	in.WorkspaceKey = routerE2EWS
	if in.Name == "" {
		in.Name = in.BindingID
	}
	if in.SourceKind == "" {
		in.SourceKind = "github"
	}
	in.DriverID = "pr-review"
	in.DriverVersionID = "v1"
	in.TargetEntrypoint = "run"
	in.Enabled = true
	if _, err := st.TriggerBindings().Create(context.Background(), in); err != nil {
		t.Fatalf("seed binding %s: %v", in.BindingID, err)
	}
}

func routerE2EMux(st store.Store) *http.ServeMux {
	mux := http.NewServeMux()
	NewModule(st).Register(mux)
	return mux
}

// routerE2EPRBody builds a GitHub pull_request payload for PR acme/widgets#7
// with the given action and head SHA (base ref pinned to main so attrs-based
// subject keys collide across a synchronize storm).
func routerE2EPRBody(action, headSHA string) []byte {
	return fmt.Appendf(nil,
		`{"action":%q,"pull_request":{"number":7,"head":{"sha":%q},"base":{"ref":"main"}},"repository":{"full_name":"acme/widgets"},"sender":{"login":"octocat"}}`,
		action, headSHA)
}

// routerE2EResponse is the decoded 202 wire plus the raw body keys, so tests
// can pin both the typed legs and the BREAKING deliveries[]-only shape.
type routerE2EResponse struct {
	Status         string                       `json:"status"`
	RouteKey       string                       `json:"route_key"`
	IdempotencyKey string                       `json:"idempotency_key"`
	Deliveries     []store.TriggerRouteDelivery `json:"deliveries"`

	raw map[string]json.RawMessage
}

// routerE2EPost posts a signed GitHub webhook through the mux and decodes the
// 202 response.
func routerE2EPost(t *testing.T, mux *http.ServeMux, deliveryID string, body []byte) routerE2EResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+routerE2EWS+"/webhooks/github", bytes.NewReader(body))
	req.Header.Set(githubEventHeader, "pull_request")
	req.Header.Set(githubDeliveryHeader, deliveryID)
	req.Header.Set(githubSignatureHeader, githubSignature(routerE2ESecret, body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST webhook %s: status = %d, body = %s", deliveryID, rr.Code, rr.Body.String())
	}
	var resp routerE2EResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode webhook response: %v\n%s", err, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp.raw); err != nil {
		t.Fatalf("decode raw webhook response: %v", err)
	}
	return resp
}

// routerE2EGet fetches a read-route JSON body through the mux.
func routerE2EGet(t *testing.T, mux *http.ServeMux, path string, out any) {
	t.Helper()
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d, body = %s", path, rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), out); err != nil {
		t.Fatalf("decode GET %s: %v\n%s", path, err, rr.Body.String())
	}
}

// routerE2EOnlyEvent fetches the workspace's single trigger event over HTTP.
func routerE2EOnlyEvent(t *testing.T, mux *http.ServeMux) *domain.TriggerEvent {
	t.Helper()
	var page struct {
		Events []*domain.TriggerEvent `json:"trigger_events"`
		Count  int                    `json:"count"`
	}
	routerE2EGet(t, mux, "/api/workspaces/"+routerE2EWS+"/trigger-events", &page)
	if page.Count != 1 || len(page.Events) != 1 {
		t.Fatalf("trigger events = %d, want exactly 1: %+v", page.Count, page.Events)
	}
	return page.Events[0]
}

// routerE2EDelivery fetches one persisted delivery over HTTP.
func routerE2EDelivery(t *testing.T, mux *http.ServeMux, deliveryID string) *domain.TriggerDelivery {
	t.Helper()
	var delivery domain.TriggerDelivery
	routerE2EGet(t, mux, "/api/workspaces/"+routerE2EWS+"/trigger-deliveries/"+deliveryID, &delivery)
	return &delivery
}

// routerE2ECounts snapshots the workspace's event / run / delivery totals.
func routerE2ECounts(t *testing.T, st *memstore.Store) (events, runs, deliveries int) {
	t.Helper()
	ctx := context.Background()
	evs, err := st.TriggerEvents().List(ctx, routerE2EWS, store.TriggerEventFilter{})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	rns, err := st.DriverRuns().List(ctx, routerE2EWS, store.DriverRunFilter{})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	dels, err := st.TriggerDeliveries().List(ctx, routerE2EWS, store.TriggerDeliveryFilter{})
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	return len(evs), len(rns), len(dels)
}

// routerE2EFinishRun drives a queued run to completed through the public
// claim/finish lane, freeing its concurrency subject.
func routerE2EFinishRun(t *testing.T, st *memstore.Store, runID string) {
	t.Helper()
	ctx := context.Background()
	claimed, err := st.DriverRuns().Claim(ctx, routerE2EWS, runID, "node-1", "lease-1")
	if err != nil {
		t.Fatalf("claim run %s: %v", runID, err)
	}
	if _, err := st.DriverRuns().Finish(ctx, routerE2EWS, runID, store.DriverRunFinish{
		NodeID: "node-1", LeaseID: "lease-1", FencingToken: claimed.FencingToken,
		Status: domain.DriverRunCompleted,
	}); err != nil {
		t.Fatalf("finish run %s: %v", runID, err)
	}
}

// TestRouterE2EWebhookFanOutTraceAndOrigin: a signed pull_request.opened
// webhook fans out to the exact-RouteKey owner plus an alternation-pattern
// binding, the 202 carries deliveries[] only, and the full
// Event -> Delivery -> Run trace is walkable by id over the read routes —
// with structural origin stamping (external, hop depth 0) on the event.
func TestRouterE2EWebhookFanOutTraceAndOrigin(t *testing.T) {
	st := routerE2EStore(t)
	routerE2EBinding(t, st, store.TriggerBindingCreate{
		BindingID: "b-exact", RouteKey: "github.pull_request.opened", WebhookSecret: routerE2ESecret,
	})
	routerE2EBinding(t, st, store.TriggerBindingCreate{
		BindingID:         "b-pattern",
		EventTypePatterns: []string{"github.pull_request.{opened,synchronize,reopened,ready_for_review}"},
	})
	mux := routerE2EMux(st)
	ctx := context.Background()

	resp := routerE2EPost(t, mux, "fan-1", routerE2EPRBody("opened", "sha-fan"))
	if resp.Status != "accepted" || resp.RouteKey != "github.pull_request.opened" || resp.IdempotencyKey != "github:fan-1" {
		t.Fatalf("response envelope = %+v", resp)
	}
	// BREAKING router-v2 wire: deliveries[] only, no top-level run keys.
	for _, key := range []string{"driver_run_id", "driver_run"} {
		if _, ok := resp.raw[key]; ok {
			t.Errorf("202 body still carries removed top-level key %q", key)
		}
	}
	if len(resp.Deliveries) != 2 {
		t.Fatalf("want 2 fan-out legs, got %d: %+v", len(resp.Deliveries), resp.Deliveries)
	}
	// Exact RouteKey owner first, pattern matches after (binding-id order).
	if resp.Deliveries[0].BindingID != "b-exact" || resp.Deliveries[1].BindingID != "b-pattern" {
		t.Fatalf("leg order = %+v, want exact owner first", resp.Deliveries)
	}
	if resp.Deliveries[0].RunID == resp.Deliveries[1].RunID {
		t.Fatalf("fan-out legs share run id %q", resp.Deliveries[0].RunID)
	}

	// One persisted event with structural provenance: external at hop 0.
	event := routerE2EOnlyEvent(t, mux)
	if event.SourceEventID != "fan-1" || event.EventType != "pull_request" ||
		event.SubjectRef != "acme/widgets#7" || event.ActorRef != "octocat" {
		t.Fatalf("event identity = %+v", event)
	}
	if event.SignatureStatus != "verified" || event.RawPayloadDigest == "" || event.IdempotencyKey != "github:fan-1" {
		t.Fatalf("event verification = %+v", event)
	}
	if event.Origin != domain.TriggerEventOriginExternal || event.HopDepth != 0 {
		t.Fatalf("event origin = %q hop %d, want external at hop 0", event.Origin, event.HopDepth)
	}

	// Walk every leg: Delivery -> Run linkage by id, per-leg composite
	// idempotency key, rendered default subject key per binding.
	for _, leg := range resp.Deliveries {
		if leg.Status != domain.TriggerDeliveryDispatched || leg.DeliveryID == "" || leg.RunID == "" {
			t.Fatalf("leg incomplete: %+v", leg)
		}
		delivery := routerE2EDelivery(t, mux, leg.DeliveryID)
		if delivery.TriggerEventID != event.EventID || delivery.TriggerBindingID != leg.BindingID ||
			delivery.DriverRunID != leg.RunID || delivery.Attempt != 1 {
			t.Fatalf("delivery linkage = %+v, want event=%s binding=%s run=%s", delivery, event.EventID, leg.BindingID, leg.RunID)
		}
		if want := leg.BindingID + "|acme/widgets#7"; delivery.SubjectKey != want {
			t.Errorf("delivery subject key = %q, want %q", delivery.SubjectKey, want)
		}
		run, err := st.DriverRuns().Get(ctx, routerE2EWS, leg.RunID)
		if err != nil {
			t.Fatalf("get run %s: %v", leg.RunID, err)
		}
		if run.Status != domain.DriverRunQueued || run.DriverID != "pr-review" || run.SourceRef != event.EventID {
			t.Fatalf("run = %+v, want queued pr-review for event %s", run, event.EventID)
		}
		// Fan-out idempotency (locked decision): composite {key}#{bindingID}.
		if want := "github:fan-1#" + leg.BindingID; run.IdempotencyKey != want {
			t.Errorf("run idempotency key = %q, want %q", run.IdempotencyKey, want)
		}
		var payload struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(run.Payload, &payload); err != nil || payload.Action != "opened" {
			t.Errorf("run payload = %s (err %v), want original webhook body", run.Payload, err)
		}
	}

	// Redelivery heals every leg to the same identifiers, creating no state.
	replay := routerE2EPost(t, mux, "fan-1", routerE2EPRBody("opened", "sha-fan"))
	if len(replay.Deliveries) != 2 {
		t.Fatalf("replay legs = %d, want 2", len(replay.Deliveries))
	}
	for i := range replay.Deliveries {
		if replay.Deliveries[i] != resp.Deliveries[i] {
			t.Errorf("replay leg %d = %+v, want %+v", i, replay.Deliveries[i], resp.Deliveries[i])
		}
	}
	if events, runs, deliveries := routerE2ECounts(t, st); events != 1 || runs != 2 || deliveries != 2 {
		t.Fatalf("state after replay: events=%d runs=%d deliveries=%d, want 1/2/2", events, runs, deliveries)
	}
}

// TestRouterE2ELegacyExactLaneStable pins the Tier-0 loom-dev contract on the
// single-binding exact lane: the 202 envelope carries exactly the four
// router-v2 keys, the run keeps the BARE ingress idempotency key and the
// delivery keeps the delivery-{eventID} id (locked decision), and redelivery
// heals onto identical identifiers.
func TestRouterE2ELegacyExactLaneStable(t *testing.T) {
	st := routerE2EStore(t)
	routerE2EBinding(t, st, store.TriggerBindingCreate{
		BindingID: "b-echo", RouteKey: "github.pull_request.opened", WebhookSecret: routerE2ESecret,
	})
	mux := routerE2EMux(st)

	resp := routerE2EPost(t, mux, "tier0-1", routerE2EPRBody("opened", "sha-t0"))
	for _, key := range []string{"status", "route_key", "idempotency_key", "deliveries"} {
		if _, ok := resp.raw[key]; !ok {
			t.Errorf("202 body missing key %q", key)
		}
	}
	if len(resp.raw) != 4 {
		t.Errorf("202 body has %d top-level keys, want exactly 4: %v", len(resp.raw), resp.raw)
	}
	if len(resp.Deliveries) != 1 {
		t.Fatalf("legacy lane legs = %d, want 1: %+v", len(resp.Deliveries), resp.Deliveries)
	}
	leg := resp.Deliveries[0]
	event := routerE2EOnlyEvent(t, mux)
	if want := "delivery-" + event.EventID; leg.DeliveryID != want {
		t.Errorf("legacy delivery id = %q, want %q", leg.DeliveryID, want)
	}
	run, err := st.DriverRuns().Get(context.Background(), routerE2EWS, leg.RunID)
	if err != nil {
		t.Fatalf("get run %s: %v", leg.RunID, err)
	}
	// Bare key, NOT composite — loom-dev healing depends on this staying put.
	if run.IdempotencyKey != "github:tier0-1" {
		t.Errorf("legacy run idempotency key = %q, want bare github:tier0-1", run.IdempotencyKey)
	}

	replay := routerE2EPost(t, mux, "tier0-1", routerE2EPRBody("opened", "sha-t0"))
	if len(replay.Deliveries) != 1 || replay.Deliveries[0] != leg {
		t.Fatalf("replay leg = %+v, want %+v", replay.Deliveries, leg)
	}
	if events, runs, deliveries := routerE2ECounts(t, st); events != 1 || runs != 1 || deliveries != 1 {
		t.Fatalf("state after replay: events=%d runs=%d deliveries=%d, want 1/1/1", events, runs, deliveries)
	}
}

// TestRouterE2EReplaceSupersedeStorm: the GitHub-review shape — a
// replace-policy binding with an attrs subject template collapses a
// synchronize storm of 3 webhooks for the same PR to one queued run, with the
// two losers cancelled and their deliveries superseded for audit. A replay of
// a superseded delivery reports superseded and resurrects nothing.
func TestRouterE2EReplaceSupersedeStorm(t *testing.T) {
	st := routerE2EStore(t)
	routerE2EBinding(t, st, store.TriggerBindingCreate{
		BindingID: "b-replace", RouteKey: "github.pull_request.synchronize", WebhookSecret: routerE2ESecret,
		ConcurrencyPolicy:  domain.TriggerBindingConcurrencyReplace,
		SubjectKeyTemplate: "{{subject_ref}}@{{attrs.base_ref}}",
	})
	mux := routerE2EMux(st)
	ctx := context.Background()
	const subjectKey = "acme/widgets#7@main"

	legs := make([]store.TriggerRouteDelivery, 0, 3)
	for i, sha := range []string{"sha-aaa", "sha-bbb", "sha-ccc"} {
		resp := routerE2EPost(t, mux, fmt.Sprintf("storm-%d", i+1), routerE2EPRBody("synchronize", sha))
		if len(resp.Deliveries) != 1 || resp.Deliveries[0].Status != domain.TriggerDeliveryDispatched {
			t.Fatalf("storm post %d legs = %+v, want one dispatched leg", i+1, resp.Deliveries)
		}
		legs = append(legs, resp.Deliveries[0])
	}

	// Newest run queued; the two older queued runs cancelled as superseded.
	for i, leg := range legs[:2] {
		run, err := st.DriverRuns().Get(ctx, routerE2EWS, leg.RunID)
		if err != nil || run.Status != domain.DriverRunCancelled || run.ErrorClass != "superseded" {
			t.Fatalf("storm run %d = %+v (err %v), want cancelled/superseded", i+1, run, err)
		}
	}
	winner, err := st.DriverRuns().Get(ctx, routerE2EWS, legs[2].RunID)
	if err != nil || winner.Status != domain.DriverRunQueued {
		t.Fatalf("winner run = %+v (err %v), want queued", winner, err)
	}

	// Loser deliveries audit as superseded, carrying the rendered subject key.
	var superseded struct {
		Deliveries []*domain.TriggerDelivery `json:"trigger_deliveries"`
		Count      int                       `json:"count"`
	}
	routerE2EGet(t, mux, "/api/workspaces/"+routerE2EWS+"/trigger-deliveries?status=superseded", &superseded)
	if superseded.Count != 2 {
		t.Fatalf("superseded deliveries = %d, want 2: %+v", superseded.Count, superseded.Deliveries)
	}
	for i, leg := range legs[:2] {
		delivery := routerE2EDelivery(t, mux, leg.DeliveryID)
		if delivery.Status != domain.TriggerDeliverySuperseded || delivery.SubjectKey != subjectKey || delivery.DriverRunID != leg.RunID {
			t.Fatalf("loser delivery %d = %+v, want superseded for %q run %s", i+1, delivery, subjectKey, leg.RunID)
		}
	}
	if delivery := routerE2EDelivery(t, mux, legs[2].DeliveryID); delivery.Status != domain.TriggerDeliveryDispatched || delivery.SubjectKey != subjectKey {
		t.Fatalf("winner delivery = %+v, want dispatched for %q", delivery, subjectKey)
	}

	// Replay of a loser: reports the recorded superseded state, leaves the
	// winner queued and the store untouched.
	replay := routerE2EPost(t, mux, "storm-2", routerE2EPRBody("synchronize", "sha-bbb"))
	if len(replay.Deliveries) != 1 {
		t.Fatalf("replay legs = %+v, want 1", replay.Deliveries)
	}
	if leg := replay.Deliveries[0]; leg.Status != domain.TriggerDeliverySuperseded || leg.RunID != legs[1].RunID {
		t.Fatalf("replayed loser leg = %+v, want superseded run %s", leg, legs[1].RunID)
	}
	if run, err := st.DriverRuns().Get(ctx, routerE2EWS, legs[2].RunID); err != nil || run.Status != domain.DriverRunQueued {
		t.Fatalf("winner after replay = %+v (err %v), want still queued", run, err)
	}
	if events, runs, deliveries := routerE2ECounts(t, st); events != 3 || runs != 3 || deliveries != 3 {
		t.Fatalf("storm state: events=%d runs=%d deliveries=%d, want 3/3/3", events, runs, deliveries)
	}
}

// TestRouterE2EForbidRejectsAndQueuePromotesViaSweeper: with the subject busy,
// a second webhook is rejected on the forbid leg (no run, audit
// concurrency_forbid) and held held on the queue leg; once the blocking run
// completes, one sweeper pass promotes the held delivery onto a fresh run
// while the forbid rejection stays final.
func TestRouterE2EForbidRejectsAndQueuePromotesViaSweeper(t *testing.T) {
	st := routerE2EStore(t)
	routerE2EBinding(t, st, store.TriggerBindingCreate{
		BindingID: "b-forbid", RouteKey: "github.pull_request.opened", WebhookSecret: routerE2ESecret,
		ConcurrencyPolicy: domain.TriggerBindingConcurrencyForbid,
	})
	routerE2EBinding(t, st, store.TriggerBindingCreate{
		BindingID:           "b-queue",
		EventTypePatterns:   []string{"github.pull_request.opened"},
		ConcurrencyPolicy:   domain.TriggerBindingConcurrencyQueue,
		RetryBackoffSeconds: 30,
	})
	mux := routerE2EMux(st)
	ctx := context.Background()

	// First webhook: subject free, both legs dispatch queued runs.
	first := routerE2EPost(t, mux, "fq-1", routerE2EPRBody("opened", "sha-1"))
	if len(first.Deliveries) != 2 || first.Deliveries[0].Status != domain.TriggerDeliveryDispatched ||
		first.Deliveries[1].Status != domain.TriggerDeliveryDispatched {
		t.Fatalf("first webhook legs = %+v, want both dispatched", first.Deliveries)
	}
	queueRunID := first.Deliveries[1].RunID

	// Second webhook for the same PR: forbid rejects, queue queues held —
	// both gated BEFORE any run exists.
	second := routerE2EPost(t, mux, "fq-2", routerE2EPRBody("opened", "sha-2"))
	if len(second.Deliveries) != 2 {
		t.Fatalf("second webhook legs = %+v, want 2", second.Deliveries)
	}
	forbidLeg, queueLeg := second.Deliveries[0], second.Deliveries[1]
	if forbidLeg.Status != domain.TriggerDeliveryRejected || forbidLeg.RejectionReason != "concurrency_forbid" || forbidLeg.RunID != "" {
		t.Fatalf("forbid leg = %+v, want rejected concurrency_forbid without run", forbidLeg)
	}
	if queueLeg.Status != domain.TriggerDeliveryHeld || queueLeg.RunID != "" {
		t.Fatalf("queue leg = %+v, want held without run", queueLeg)
	}
	held := routerE2EDelivery(t, mux, queueLeg.DeliveryID)
	if held.Status != domain.TriggerDeliveryHeld || held.Attempt != 1 || held.NextRetryAt == nil {
		t.Fatalf("held delivery = %+v, want held attempt 1 with next_retry_at", held)
	}
	if rejected := routerE2EDelivery(t, mux, forbidLeg.DeliveryID); rejected.Status != domain.TriggerDeliveryRejected ||
		rejected.RejectionReason != "concurrency_forbid" || rejected.DriverRunID != "" {
		t.Fatalf("rejected delivery = %+v, want audited forbid rejection", rejected)
	}
	if events, runs, deliveries := routerE2ECounts(t, st); events != 2 || runs != 2 || deliveries != 4 {
		t.Fatalf("gated state: events=%d runs=%d deliveries=%d, want 2/2/4", events, runs, deliveries)
	}

	// Free the queue binding's subject, then sweep at the due instant: the
	// held delivery re-enters admission and promotes onto a fresh run.
	routerE2EFinishRun(t, st, queueRunID)
	sweeper := &trigger.DeliverySweeper{Store: st, WorkspaceKey: routerE2EWS}
	now := held.NextRetryAt.Add(time.Second)
	sweeper.Now = func() time.Time { return now }
	result, err := sweeper.RunOnce(ctx)
	if err != nil {
		t.Fatalf("sweeper RunOnce: %v", err)
	}
	if result.Dispatched != 1 || result.Rescheduled != 0 || result.Exhausted != 0 {
		t.Fatalf("sweep result = %+v, want exactly one promotion", result)
	}

	promoted := routerE2EDelivery(t, mux, queueLeg.DeliveryID)
	if promoted.Status != domain.TriggerDeliveryDispatched || promoted.Attempt != 2 ||
		promoted.NextRetryAt != nil || promoted.DriverRunID == "" {
		t.Fatalf("promoted delivery = %+v, want dispatched attempt 2 with run", promoted)
	}
	if run, err := st.DriverRuns().Get(ctx, routerE2EWS, promoted.DriverRunID); err != nil || run.Status != domain.DriverRunQueued {
		t.Fatalf("promoted run = %+v (err %v), want queued", run, err)
	}
	// The forbid rejection is final: same recorded state, still no run.
	if rejected := routerE2EDelivery(t, mux, forbidLeg.DeliveryID); rejected.Status != domain.TriggerDeliveryRejected || rejected.DriverRunID != "" {
		t.Fatalf("rejected delivery after sweep = %+v, want unchanged", rejected)
	}
	if events, runs, deliveries := routerE2ECounts(t, st); events != 2 || runs != 3 || deliveries != 4 {
		t.Fatalf("post-sweep state: events=%d runs=%d deliveries=%d, want 2/3/4", events, runs, deliveries)
	}
}

// TestRouterE2ECronTickDispatchAndReplicaDedup: a due cron tick dispatches one
// run through the normal router path, a second RunOnce on the same scheduler
// does not re-fire (window advanced), and an overlapping replica scheduler
// firing the same instant dedups on the cron:{binding}:{fireUnix} idempotency
// key — no duplicate event, run or delivery.
func TestRouterE2ECronTickDispatchAndReplicaDedup(t *testing.T) {
	st := routerE2EStore(t)
	routerE2EBinding(t, st, store.TriggerBindingCreate{
		BindingID: "b-cron", SourceKind: "cron", RouteKey: "cron.nightly.report",
		Schedule: "* * * * *",
	})
	mux := routerE2EMux(st)
	ctx := context.Background()

	t0 := time.Date(2026, 6, 12, 10, 0, 30, 0, time.UTC)
	fire := time.Date(2026, 6, 12, 10, 1, 0, 0, time.UTC) // next minute boundary
	tickKey := trigger.CronTickIdempotencyKey("b-cron", fire)
	primary := &trigger.CronScheduler{Store: st, WorkspaceKey: routerE2EWS}
	replica := &trigger.CronScheduler{Store: st, WorkspaceKey: routerE2EWS}

	// First observation primes the window without firing historical ticks.
	for name, sched := range map[string]*trigger.CronScheduler{"primary": primary, "replica": replica} {
		result, err := sched.RunOnce(ctx, t0)
		if err != nil || result.Fired != 0 {
			t.Fatalf("%s prime sweep = %+v (err %v), want no fire", name, result, err)
		}
	}

	// Due sweep fires exactly one tick; the immediate re-sweep is a no-op.
	now := t0.Add(90 * time.Second)
	if result, err := primary.RunOnce(ctx, now); err != nil || result.Fired != 1 {
		t.Fatalf("due sweep = %+v (err %v), want one fire", result, err)
	}
	if result, err := primary.RunOnce(ctx, now); err != nil || result.Fired != 0 {
		t.Fatalf("double RunOnce = %+v (err %v), want no re-fire", result, err)
	}
	// The replica fires the same instant; the dispatch dedups end-to-end.
	if result, err := replica.RunOnce(ctx, now); err != nil || result.Fired != 1 {
		t.Fatalf("replica sweep = %+v (err %v), want fire attempt", result, err)
	}
	if events, runs, deliveries := routerE2ECounts(t, st); events != 1 || runs != 1 || deliveries != 1 {
		t.Fatalf("cron state: events=%d runs=%d deliveries=%d, want 1/1/1", events, runs, deliveries)
	}

	// The synthetic event rode the normal router path: cron.tick identity,
	// deterministic tick idempotency key, route-dispatch origin stamping.
	event := routerE2EOnlyEvent(t, mux)
	if event.EventType != trigger.CronEventType || event.ActorRef != trigger.CronActorRef || event.SubjectRef != "b-cron" {
		t.Fatalf("cron event identity = %+v", event)
	}
	if event.IdempotencyKey != tickKey || event.SourceEventID != tickKey {
		t.Fatalf("cron event keys = %+v, want %q", event, tickKey)
	}
	if event.Origin != domain.TriggerEventOriginExternal || event.HopDepth != 0 {
		t.Fatalf("cron event origin = %q hop %d, want route-dispatch external at hop 0", event.Origin, event.HopDepth)
	}

	var page struct {
		Deliveries []*domain.TriggerDelivery `json:"trigger_deliveries"`
	}
	routerE2EGet(t, mux, "/api/workspaces/"+routerE2EWS+"/trigger-deliveries", &page)
	if len(page.Deliveries) != 1 || page.Deliveries[0].Status != domain.TriggerDeliveryDispatched {
		t.Fatalf("cron deliveries = %+v, want one dispatched", page.Deliveries)
	}
	run, err := st.DriverRuns().Get(ctx, routerE2EWS, page.Deliveries[0].DriverRunID)
	if err != nil || run.Status != domain.DriverRunQueued {
		t.Fatalf("cron run = %+v (err %v), want queued", run, err)
	}
	// Cron binding is the exact route owner: legacy lane keeps the bare key.
	if run.IdempotencyKey != tickKey {
		t.Fatalf("cron run idempotency key = %q, want %q", run.IdempotencyKey, tickKey)
	}
	var payload struct {
		Tick string `json:"tick"`
	}
	if err := json.Unmarshal(run.Payload, &payload); err != nil || payload.Tick != fire.Format(time.RFC3339) {
		t.Fatalf("cron run payload = %s (err %v), want tick %s", run.Payload, err, fire.Format(time.RFC3339))
	}
}
