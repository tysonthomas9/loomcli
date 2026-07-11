package memstore

import (
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// fanOutBinding is one row of the binding fixture table for the fan-out tests,
// mirroring fleet-db's setupFanOutBindings.
type fanOutBinding struct {
	bindingID         string
	routeKey          string
	eventTypePatterns []string
	disabled          bool
}

func setupFanOutBindings(t *testing.T, s *Store, bindings []fanOutBinding) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "pr-review", Name: "pr-review",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "v1", DriverID: "pr-review", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b", ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	for _, b := range bindings {
		if _, err := s.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
			WorkspaceKey: "WS", BindingID: b.bindingID, Name: b.bindingID, SourceKind: "github",
			RouteKey: b.routeKey, EventTypePatterns: b.eventTypePatterns,
			DriverID: "pr-review", DriverVersionID: "v1", TargetEntrypoint: "run",
			Enabled: !b.disabled,
		}); err != nil {
			t.Fatalf("Create trigger binding %s: %v", b.bindingID, err)
		}
	}
}

func fanOutState(s *Store) (runs, deliveries, events int) {
	return len(s.runs.items["WS"]), len(s.deliveries.items["WS"]), len(s.events.items["WS"])
}

// TestDispatchTriggerRouteFanOutDispatch mirrors fleet-db's
// TestPlatformAPITriggerRouteFanOutDispatch matrix: one ingress event
// dispatches one delivery+run per matched binding (exact route-key owner
// first, then pattern matches in binding-id order) with deterministic per-leg
// ids and composite {idempotencyKey}#{bindingID} run keys. Disabled bindings
// are skipped; zero matches stay not-found.
func TestDispatchTriggerRouteFanOutDispatch(t *testing.T) {
	ctx := t.Context()
	s := New()
	setupFanOutBindings(t, s, []fanOutBinding{
		{bindingID: "binding-exact", routeKey: "github.pull_request.opened"},
		{bindingID: "binding-pattern-a", routeKey: "lane.pattern.a", eventTypePatterns: []string{"github.pull_request.*"}},
		{bindingID: "binding-pattern-b", routeKey: "lane.pattern.b", eventTypePatterns: []string{"github.{pull_request,push}.opened"}},
		{bindingID: "binding-pattern-off", routeKey: "lane.pattern.off", eventTypePatterns: []string{"github.*.*"}, disabled: true},
	})

	in := store.TriggerRouteDispatch{
		IdempotencyKey: "fan-1", EventType: "pull_request", SubjectRef: "acme/widgets#7",
	}
	result, err := s.TriggerRoutes().DispatchTriggerRouteV2(ctx, "WS", "github.pull_request.opened", in)
	if err != nil {
		t.Fatalf("DispatchTriggerRouteV2: %v", err)
	}
	if len(result.Deliveries) != 3 {
		t.Fatalf("deliveries = %#v, want 3 legs (disabled binding skipped)", result.Deliveries)
	}
	wantOrder := []string{"binding-exact", "binding-pattern-a", "binding-pattern-b"}
	for i, want := range wantOrder {
		if result.Deliveries[i].BindingID != want {
			t.Fatalf("delivery[%d] binding = %q, want %q (exact first, then patterns by binding id)", i, result.Deliveries[i].BindingID, want)
		}
		if result.Deliveries[i].Status != domain.TriggerDeliveryDispatched {
			t.Fatalf("delivery[%d] status = %q, want dispatched", i, result.Deliveries[i].Status)
		}
	}
	if runs, deliveries, events := fanOutState(s); runs != 3 || deliveries != 3 || events != 1 {
		t.Fatalf("state: runs=%d deliveries=%d events=%d, want 3/3/1 (one shared event)", runs, deliveries, events)
	}
	if result.PrimaryRun == nil || result.PrimaryRun.RunID != result.Deliveries[0].RunID {
		t.Fatalf("primary run = %#v, want the exact leg's run %q", result.PrimaryRun, result.Deliveries[0].RunID)
	}
	eventID := result.PrimaryRun.SourceRef
	seenRuns := map[string]bool{}
	for i, leg := range result.Deliveries {
		if leg.DeliveryID != "delivery-"+eventID+"-"+leg.BindingID {
			t.Fatalf("delivery[%d] id = %q, want delivery-%s-%s", i, leg.DeliveryID, eventID, leg.BindingID)
		}
		if leg.RunID != "run-"+shortTriggerHash(eventID, leg.BindingID) {
			t.Fatalf("delivery[%d] run id = %q, want deterministic run-%s", i, leg.RunID, shortTriggerHash(eventID, leg.BindingID))
		}
		run, err := s.DriverRuns().Get(ctx, "WS", leg.RunID)
		if err != nil {
			t.Fatalf("run %q was not recorded: %v", leg.RunID, err)
		}
		if run.Status != domain.DriverRunQueued || run.SourceRef != eventID {
			t.Fatalf("run %q = %+v, want queued on event %s", leg.RunID, run, eventID)
		}
		if run.IdempotencyKey != "fan-1#"+leg.BindingID {
			t.Fatalf("run %q idempotency key = %q, want composite fan-1#%s", leg.RunID, run.IdempotencyKey, leg.BindingID)
		}
		if seenRuns[leg.RunID] {
			t.Fatalf("run id %q reused across legs", leg.RunID)
		}
		seenRuns[leg.RunID] = true
		if _, err := s.TriggerDeliveries().Get(ctx, "WS", leg.DeliveryID); err != nil {
			t.Fatalf("delivery %q was not recorded: %v", leg.DeliveryID, err)
		}
	}

	// Redelivery: deterministic ids are stable and nothing is duplicated.
	replay, err := s.TriggerRoutes().DispatchTriggerRouteV2(ctx, "WS", "github.pull_request.opened", in)
	if err != nil {
		t.Fatalf("DispatchTriggerRouteV2 replay: %v", err)
	}
	if len(replay.Deliveries) != 3 {
		t.Fatalf("replay deliveries = %#v, want 3 legs", replay.Deliveries)
	}
	for i := range result.Deliveries {
		if replay.Deliveries[i] != result.Deliveries[i] {
			t.Fatalf("replay delivery[%d] = %#v, want stable %#v", i, replay.Deliveries[i], result.Deliveries[i])
		}
	}
	if runs, deliveries, events := fanOutState(s); runs != 3 || deliveries != 3 || events != 1 {
		t.Fatalf("replay duplicated state: runs=%d deliveries=%d events=%d, want 3/3/1", runs, deliveries, events)
	}

	// A key matched only by a pattern (no exact owner) dispatches via the
	// pattern lane with a composite key — never the bare legacy key.
	pushResult, err := s.TriggerRoutes().DispatchTriggerRouteV2(ctx, "WS", "github.push.opened", store.TriggerRouteDispatch{
		IdempotencyKey: "fan-2", EventType: "push",
	})
	if err != nil {
		t.Fatalf("DispatchTriggerRouteV2 pattern-only: %v", err)
	}
	if len(pushResult.Deliveries) != 1 || pushResult.Deliveries[0].BindingID != "binding-pattern-b" {
		t.Fatalf("pattern-only dispatch = %#v, want binding-pattern-b only", pushResult.Deliveries)
	}
	if run, err := s.DriverRuns().Get(ctx, "WS", pushResult.Deliveries[0].RunID); err != nil || run.IdempotencyKey != "fan-2#binding-pattern-b" {
		t.Fatalf("pattern-only run = %+v (err %v), want composite key fan-2#binding-pattern-b", run, err)
	}

	// No exact owner and no pattern match keeps the legacy not-found contract.
	if _, err := s.TriggerRoutes().DispatchTriggerRouteV2(ctx, "WS", "gitlab.merge_request.opened", store.TriggerRouteDispatch{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("zero-match dispatch err = %v, want ErrNotFound", err)
	}
}

// TestDispatchTriggerRouteLegacyExactPathKeepsBareKey pins the legacy
// single-binding exact lane (locked decision): a route matched only by its
// exact RouteKey owner keeps the caller-supplied run id, the bare idempotency
// key, and the delivery-{eventID} id, so pre-fan-out healing stays stable. The
// DispatchTriggerRoute wrapper returns that primary run.
func TestDispatchTriggerRouteLegacyExactPathKeepsBareKey(t *testing.T) {
	ctx := t.Context()
	s := New()
	setupFanOutBindings(t, s, []fanOutBinding{
		{bindingID: "binding-exact", routeKey: "github.pull_request.opened"},
	})

	run, err := s.TriggerRoutes().DispatchTriggerRoute(ctx, "WS", "github.pull_request.opened", store.TriggerRouteDispatch{
		RunID: "run-legacy", IdempotencyKey: "leg-1", EventType: "pull_request", SubjectRef: "acme/widgets#7",
	})
	if err != nil {
		t.Fatalf("DispatchTriggerRoute: %v", err)
	}
	if run.RunID != "run-legacy" || run.IdempotencyKey != "leg-1" {
		t.Fatalf("legacy run = %+v, want caller run id and bare idempotency key", run)
	}
	if _, err := s.TriggerDeliveries().Get(ctx, "WS", "delivery-"+run.SourceRef); err != nil {
		t.Fatalf("legacy delivery delivery-%s was not recorded: %v", run.SourceRef, err)
	}
	if runs, deliveries, events := fanOutState(s); runs != 1 || deliveries != 1 || events != 1 {
		t.Fatalf("state: runs=%d deliveries=%d events=%d, want 1/1/1", runs, deliveries, events)
	}
}

// TestDispatchTriggerRouteFanOutRedeliveryHealsPartialFailure mirrors
// fleet-db's matrix: a one-shot delivery write failure on the pattern leg
// fails the first dispatch after the exact leg (and the pattern leg's run)
// are durable, and the redelivery heals the missing delivery without
// duplicating any state. The deterministic per-leg ids are stable across the
// retry.
func TestDispatchTriggerRouteFanOutRedeliveryHealsPartialFailure(t *testing.T) {
	ctx := t.Context()
	s := New()
	setupFanOutBindings(t, s, []fanOutBinding{
		{bindingID: "binding-exact", routeKey: "github.pull_request.opened"},
		{bindingID: "binding-pattern", routeKey: "lane.pattern.heal", eventTypePatterns: []string{"github.pull_request.*"}},
	})

	failures := 0
	s.deliveries.failCreate = func(delivery *domain.TriggerDelivery) error {
		if delivery.TriggerBindingID == "binding-pattern" && failures == 0 {
			failures++
			return errors.New("injected delivery write failure")
		}
		return nil
	}

	in := store.TriggerRouteDispatch{
		IdempotencyKey: "heal-1", EventType: "pull_request", SubjectRef: "acme/widgets#7",
	}
	if _, err := s.TriggerRoutes().DispatchTriggerRouteV2(ctx, "WS", "github.pull_request.opened", in); err == nil {
		t.Fatal("DispatchTriggerRouteV2 with injected failure succeeded, want error")
	}

	// Partial state: both runs admitted, only the exact leg's delivery written.
	if runs, deliveries, events := fanOutState(s); runs != 2 || deliveries != 1 || events != 1 {
		t.Fatalf("partial failure state: runs=%d deliveries=%d events=%d, want 2/1/1", runs, deliveries, events)
	}
	firstRunIDs := map[string]string{}
	for id, run := range s.runs.items["WS"] {
		firstRunIDs[run.IdempotencyKey] = id
	}

	result, err := s.TriggerRoutes().DispatchTriggerRouteV2(ctx, "WS", "github.pull_request.opened", in)
	if err != nil {
		t.Fatalf("DispatchTriggerRouteV2 retry: %v", err)
	}
	if len(result.Deliveries) != 2 {
		t.Fatalf("healed deliveries = %#v, want 2 legs", result.Deliveries)
	}
	if runs, deliveries, events := fanOutState(s); runs != 2 || deliveries != 2 || events != 1 {
		t.Fatalf("healed state: runs=%d deliveries=%d events=%d, want 2/2/1 (no duplicates)", runs, deliveries, events)
	}
	for _, leg := range result.Deliveries {
		want, ok := firstRunIDs["heal-1#"+leg.BindingID]
		if !ok || leg.RunID != want {
			t.Fatalf("leg %s run id = %q, want stable %q across retry", leg.BindingID, leg.RunID, want)
		}
		if _, err := s.TriggerDeliveries().Get(ctx, "WS", leg.DeliveryID); err != nil {
			t.Fatalf("delivery %q was not recorded after healing: %v", leg.DeliveryID, err)
		}
	}
}
