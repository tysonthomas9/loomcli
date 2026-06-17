// Delivery sweeper tests live in the external trigger_test package so they
// can drive the real memstore dispatch path (memstore imports trigger for the
// pattern engine, so an internal test would be an import cycle).
package trigger_test

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

const sweepRouteKey = "github.pr.sync"

// setupSweeperBinding seeds a memstore with a driver, a version and one
// trigger binding (mirrors the cron/fan-out test fixtures).
func setupSweeperBinding(t *testing.T, s *memstore.Store, binding store.TriggerBindingCreate) {
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
	binding.WorkspaceKey = "WS"
	binding.Name = binding.BindingID
	binding.SourceKind = "github"
	binding.RouteKey = sweepRouteKey
	binding.DriverID = "pr-review"
	binding.DriverVersionID = "v1"
	binding.TargetEntrypoint = "run"
	binding.Enabled = true
	if _, err := s.TriggerBindings().Create(ctx, binding); err != nil {
		t.Fatalf("Create trigger binding %s: %v", binding.BindingID, err)
	}
}

func sweepDispatch(t *testing.T, s *memstore.Store, idem, subject string) *store.TriggerRouteDispatchResult {
	t.Helper()
	result, err := s.TriggerRoutes().DispatchTriggerRouteV2(t.Context(), "WS", sweepRouteKey, store.TriggerRouteDispatch{
		IdempotencyKey: idem, EventType: "pull_request", SubjectRef: subject,
	})
	if err != nil {
		t.Fatalf("DispatchTriggerRouteV2 %s: %v", idem, err)
	}
	return result
}

// finishRun drives a queued run to completed through the public claim/finish
// lane, freeing its concurrency subject.
func finishRun(t *testing.T, s *memstore.Store, runID string) {
	t.Helper()
	ctx := t.Context()
	claimed, err := s.DriverRuns().Claim(ctx, "WS", runID, "node-1", "lease-1")
	if err != nil {
		t.Fatalf("Claim run %s: %v", runID, err)
	}
	if _, err := s.DriverRuns().Finish(ctx, "WS", runID, store.DriverRunFinish{
		NodeID: "node-1", LeaseID: "lease-1", FencingToken: claimed.FencingToken,
		Status: domain.DriverRunCompleted,
	}); err != nil {
		t.Fatalf("Finish run %s: %v", runID, err)
	}
}

// heldDeliveryFixture dispatches two events for the same subject against a
// queue-policy binding: the first admits a run, the second queues held with
// next_retry_at = now + binding backoff. It returns the blocking run id and
// the held delivery.
func heldDeliveryFixture(t *testing.T, s *memstore.Store) (blockingRunID string, held *domain.TriggerDelivery) {
	t.Helper()
	first := sweepDispatch(t, s, "q-1", "acme/widgets#7")
	if first.PrimaryRun == nil {
		t.Fatalf("first dispatch = %+v, want admitted run", first.Deliveries)
	}
	second := sweepDispatch(t, s, "q-2", "acme/widgets#7")
	leg := second.Deliveries[0]
	if leg.Status != domain.TriggerDeliveryHeld || leg.RunID != "" {
		t.Fatalf("second dispatch leg = %+v, want held with no run", leg)
	}
	held, err := s.TriggerDeliveries().Get(t.Context(), "WS", leg.DeliveryID)
	if err != nil || held.Status != domain.TriggerDeliveryHeld || held.NextRetryAt == nil || held.Attempt != 1 {
		t.Fatalf("held delivery = %+v (err %v), want held attempt 1 with next_retry_at", held, err)
	}
	return first.PrimaryRun.RunID, held
}

func runDeliverySweep(t *testing.T, sweeper *trigger.DeliverySweeper, now time.Time) *trigger.DeliverySweepResult {
	t.Helper()
	sweeper.Now = func() time.Time { return now }
	result, err := sweeper.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce(%s): %v", now, err)
	}
	if result == nil {
		t.Fatalf("RunOnce(%s) returned nil result", now)
	}
	return result
}

func storeCounts(t *testing.T, s *memstore.Store) (events, runs, deliveries int) {
	t.Helper()
	ctx := t.Context()
	evs, err := s.TriggerEvents().List(ctx, "WS", store.TriggerEventFilter{})
	if err != nil {
		t.Fatalf("List events: %v", err)
	}
	rns, err := s.DriverRuns().List(ctx, "WS", store.DriverRunFilter{})
	if err != nil {
		t.Fatalf("List runs: %v", err)
	}
	dels, err := s.TriggerDeliveries().List(ctx, "WS", store.TriggerDeliveryFilter{})
	if err != nil {
		t.Fatalf("List deliveries: %v", err)
	}
	return len(evs), len(rns), len(dels)
}

// TestDeliverySweeperNothingDueNoOp: a sweep with no due deliveries (final
// dispatched ones only) and a sweep over zero workspaces both do nothing.
func TestDeliverySweeperNothingDueNoOp(t *testing.T) {
	st := memstore.New()
	setupSweeperBinding(t, st, store.TriggerBindingCreate{BindingID: "binding-allow"})
	if result := sweepDispatch(t, st, "ok-1", "acme/widgets#1"); result.PrimaryRun == nil {
		t.Fatalf("dispatch = %+v, want admitted run", result.Deliveries)
	}
	sweeper := &trigger.DeliverySweeper{Store: st, WorkspaceKey: "WS"}
	if result := runDeliverySweep(t, sweeper, time.Now().UTC().Add(time.Hour)); *result != (trigger.DeliverySweepResult{}) {
		t.Fatalf("sweep = %+v, want all-zero no-op", result)
	}
	if events, runs, deliveries := storeCounts(t, st); events != 1 || runs != 1 || deliveries != 1 {
		t.Fatalf("state after no-op sweep: events=%d runs=%d deliveries=%d, want 1/1/1", events, runs, deliveries)
	}

	// Unscoped against an empty store: no workspaces, nothing to sweep.
	unscoped := &trigger.DeliverySweeper{Store: memstore.New()}
	if result := runDeliverySweep(t, unscoped, time.Now().UTC()); *result != (trigger.DeliverySweepResult{}) {
		t.Fatalf("unscoped empty sweep = %+v, want all-zero no-op", result)
	}
}

// TestDeliverySweeperPromotesHeldDelivery: once the blocking run completes,
// the sweep re-dispatches the held (queue-policy) delivery with the original
// idempotency key, admits a run and promotes the delivery to dispatched —
// without duplicating the deduped trigger event.
func TestDeliverySweeperPromotesHeldDelivery(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	setupSweeperBinding(t, st, store.TriggerBindingCreate{
		BindingID: "binding-queue", ConcurrencyPolicy: domain.TriggerBindingConcurrencyQueue,
		RetryBackoffSeconds: 30,
	})
	blockingRunID, held := heldDeliveryFixture(t, st)
	finishRun(t, st, blockingRunID)

	sweeper := &trigger.DeliverySweeper{Store: st, WorkspaceKey: "WS"}
	now := held.NextRetryAt.Add(time.Second)
	result := runDeliverySweep(t, sweeper, now)
	if result.Dispatched != 1 || result.Rescheduled != 0 || result.Exhausted != 0 {
		t.Fatalf("sweep = %+v, want exactly one promotion", result)
	}

	promoted, err := st.TriggerDeliveries().Get(ctx, "WS", held.DeliveryID)
	if err != nil || promoted.Status != domain.TriggerDeliveryDispatched {
		t.Fatalf("promoted delivery = %+v (err %v), want dispatched", promoted, err)
	}
	if promoted.Attempt != 2 || promoted.NextRetryAt != nil || promoted.DriverRunID == "" {
		t.Fatalf("promoted delivery = %+v, want attempt 2, retry cleared, run stamped", promoted)
	}
	run, err := st.DriverRuns().Get(ctx, "WS", promoted.DriverRunID)
	if err != nil || run.Status != domain.DriverRunQueued {
		t.Fatalf("promoted run = %+v (err %v), want queued", run, err)
	}
	// The re-dispatch deduped the event: still 2 events, now 2 runs.
	if events, runs, deliveries := storeCounts(t, st); events != 2 || runs != 2 || deliveries != 2 {
		t.Fatalf("state after promotion: events=%d runs=%d deliveries=%d, want 2/2/2", events, runs, deliveries)
	}

	// Promoted deliveries leave the due index; the next sweep is a no-op.
	if result := runDeliverySweep(t, sweeper, now.Add(time.Hour)); *result != (trigger.DeliverySweepResult{}) {
		t.Fatalf("post-promotion sweep = %+v, want all-zero no-op", result)
	}
}

// TestDeliverySweeperHeldStillBusyBackoffDoubling: while the subject stays
// busy the held delivery is re-held with attempt++ and exponentially
// doubled backoff (base 30s held at attempt 1, then 60s, then 120s), and no
// run is admitted.
func TestDeliverySweeperHeldStillBusyBackoffDoubling(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	setupSweeperBinding(t, st, store.TriggerBindingCreate{
		BindingID: "binding-queue", ConcurrencyPolicy: domain.TriggerBindingConcurrencyQueue,
		RetryBackoffSeconds: 30,
	})
	_, held := heldDeliveryFixture(t, st)
	sweeper := &trigger.DeliverySweeper{Store: st, WorkspaceKey: "WS"}

	now := held.NextRetryAt.Add(time.Second)
	for _, want := range []struct {
		attempt int
		backoff time.Duration
	}{
		{attempt: 2, backoff: 60 * time.Second},
		{attempt: 3, backoff: 120 * time.Second},
	} {
		result := runDeliverySweep(t, sweeper, now)
		if result.Rescheduled != 1 || result.Dispatched != 0 || result.Exhausted != 0 {
			t.Fatalf("attempt %d sweep = %+v, want exactly one reschedule", want.attempt, result)
		}
		held, err := st.TriggerDeliveries().Get(ctx, "WS", held.DeliveryID)
		if err != nil || held.Status != domain.TriggerDeliveryHeld {
			t.Fatalf("attempt %d delivery = %+v (err %v), want still held", want.attempt, held, err)
		}
		if held.Attempt != want.attempt {
			t.Fatalf("attempt = %d, want %d", held.Attempt, want.attempt)
		}
		if held.NextRetryAt == nil || !held.NextRetryAt.Equal(now.Add(want.backoff)) {
			t.Fatalf("attempt %d next_retry_at = %v, want %v (now + %s doubled backoff)",
				want.attempt, held.NextRetryAt, now.Add(want.backoff), want.backoff)
		}
		now = held.NextRetryAt.Add(time.Second)
	}
	// Still exactly one run: re-holding never admits.
	if _, runs, _ := storeCounts(t, st); runs != 1 {
		t.Fatalf("runs = %d, want 1 (subject stayed busy)", runs)
	}
}

// TestDeliverySweeperRetriesExhaustedTerminal: once the attempt count reaches
// the binding's retry_max_attempts the delivery is forced terminal
// failed/retries_exhausted, leaves the due index and is never re-attempted.
func TestDeliverySweeperRetriesExhaustedTerminal(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	setupSweeperBinding(t, st, store.TriggerBindingCreate{
		BindingID: "binding-queue", ConcurrencyPolicy: domain.TriggerBindingConcurrencyQueue,
		RetryBackoffSeconds: 30, RetryMaxAttempts: 3,
	})
	_, held := heldDeliveryFixture(t, st)
	sweeper := &trigger.DeliverySweeper{Store: st, WorkspaceKey: "WS"}

	// Attempt 2: rescheduled. Attempt 3: budget spent, terminal.
	now := held.NextRetryAt.Add(time.Second)
	if result := runDeliverySweep(t, sweeper, now); result.Rescheduled != 1 {
		t.Fatalf("attempt 2 sweep = %+v, want one reschedule", result)
	}
	held, err := st.TriggerDeliveries().Get(ctx, "WS", held.DeliveryID)
	if err != nil || held.NextRetryAt == nil {
		t.Fatalf("held delivery = %+v (err %v), want rescheduled", held, err)
	}
	now = held.NextRetryAt.Add(time.Second)
	result := runDeliverySweep(t, sweeper, now)
	if result.Exhausted != 1 || result.Rescheduled != 0 || result.Dispatched != 0 {
		t.Fatalf("attempt 3 sweep = %+v, want exactly one exhaustion", result)
	}

	terminal, err := st.TriggerDeliveries().Get(ctx, "WS", held.DeliveryID)
	if err != nil || terminal.Status != domain.TriggerDeliveryFailed {
		t.Fatalf("terminal delivery = %+v (err %v), want failed", terminal, err)
	}
	if terminal.ErrorClass != domain.TriggerDeliveryErrorRetriesExhausted || terminal.NextRetryAt != nil || terminal.Attempt != 3 {
		t.Fatalf("terminal delivery = %+v, want retries_exhausted at attempt 3 with retry cleared", terminal)
	}
	due, err := st.TriggerDeliveries().ListDue(ctx, "WS", store.TriggerDeliveryDueFilter{Now: now.Add(24 * time.Hour)})
	if err != nil || len(due) != 0 {
		t.Fatalf("ListDue after exhaustion = %+v (err %v), want empty", due, err)
	}
	if result := runDeliverySweep(t, sweeper, now.Add(24*time.Hour)); *result != (trigger.DeliverySweepResult{}) {
		t.Fatalf("post-exhaustion sweep = %+v, want all-zero no-op", result)
	}
}

// TestDeliverySweeperRedispatchesFailedDelivery: a due retryable FAILED
// delivery is re-dispatched with the original idempotency key; the dispatch
// path heals the leg (run admitted) and the sweeper records it dispatched.
func TestDeliverySweeperRedispatchesFailedDelivery(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	setupSweeperBinding(t, st, store.TriggerBindingCreate{
		BindingID: "binding-queue", ConcurrencyPolicy: domain.TriggerBindingConcurrencyQueue,
		RetryBackoffSeconds: 30,
	})
	blockingRunID, held := heldDeliveryFixture(t, st)

	// Simulate a delivery-side failure recorded on the held delivery (the
	// retryable failed lane), then free the subject.
	retryAt := held.NextRetryAt.Add(time.Minute)
	failed, err := st.TriggerDeliveries().UpdateResult(ctx, "WS", held.DeliveryID, store.TriggerDeliveryResultUpdate{
		Status: domain.TriggerDeliveryFailed, Attempt: held.Attempt,
		NextRetryAt: &retryAt, ErrorClass: "delivery_target_error",
	})
	if err != nil || failed.Status != domain.TriggerDeliveryFailed {
		t.Fatalf("seed failed delivery = %+v (err %v)", failed, err)
	}
	finishRun(t, st, blockingRunID)

	sweeper := &trigger.DeliverySweeper{Store: st, WorkspaceKey: "WS"}
	now := retryAt.Add(time.Second)
	result := runDeliverySweep(t, sweeper, now)
	if result.Dispatched != 1 || result.Rescheduled != 0 || result.Exhausted != 0 {
		t.Fatalf("sweep = %+v, want exactly one redispatch", result)
	}
	healed, err := st.TriggerDeliveries().Get(ctx, "WS", held.DeliveryID)
	if err != nil || healed.Status != domain.TriggerDeliveryDispatched || healed.DriverRunID == "" {
		t.Fatalf("healed delivery = %+v (err %v), want dispatched with run", healed, err)
	}
	if healed.Attempt != 2 || healed.ErrorClass != "" || healed.NextRetryAt != nil {
		t.Fatalf("healed delivery = %+v, want attempt 2 with error and retry cleared", healed)
	}
	if run, err := st.DriverRuns().Get(ctx, "WS", healed.DriverRunID); err != nil || run.Status != domain.DriverRunQueued {
		t.Fatalf("healed run = %+v (err %v), want queued", run, err)
	}
}

// TestDeliverySweeperDispatchErrorBacksOff: a re-dispatch failure (route
// matches nothing while the binding is disabled) burns the attempt with the
// sweep_dispatch_failed class and doubled backoff; once the binding is
// re-enabled and the subject frees, the next due sweep promotes normally.
func TestDeliverySweeperDispatchErrorBacksOff(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	setupSweeperBinding(t, st, store.TriggerBindingCreate{
		BindingID: "binding-queue", ConcurrencyPolicy: domain.TriggerBindingConcurrencyQueue,
		RetryBackoffSeconds: 30,
	})
	blockingRunID, held := heldDeliveryFixture(t, st)

	setEnabled := func(enabled bool) {
		t.Helper()
		if _, err := st.TriggerBindings().Update(ctx, "WS", "binding-queue", store.TriggerBindingUpdate{Enabled: &enabled}); err != nil {
			t.Fatalf("Update binding enabled=%v: %v", enabled, err)
		}
	}
	setEnabled(false)

	sweeper := &trigger.DeliverySweeper{Store: st, WorkspaceKey: "WS"}
	now := held.NextRetryAt.Add(time.Second)
	if result := runDeliverySweep(t, sweeper, now); result.Rescheduled != 1 || result.Dispatched != 0 {
		t.Fatalf("disabled-binding sweep = %+v, want one reschedule", result)
	}
	held, err := st.TriggerDeliveries().Get(ctx, "WS", held.DeliveryID)
	if err != nil || held.Status != domain.TriggerDeliveryHeld || held.Attempt != 2 {
		t.Fatalf("held delivery = %+v (err %v), want held attempt 2", held, err)
	}
	if held.ErrorClass != "sweep_dispatch_failed" {
		t.Fatalf("held error class = %q, want sweep_dispatch_failed", held.ErrorClass)
	}
	if held.NextRetryAt == nil || !held.NextRetryAt.Equal(now.Add(60*time.Second)) {
		t.Fatalf("held next_retry_at = %v, want %v (doubled 30s backoff)", held.NextRetryAt, now.Add(60*time.Second))
	}

	// Recovery: binding back on, subject freed — the next due sweep promotes.
	setEnabled(true)
	finishRun(t, st, blockingRunID)
	now = held.NextRetryAt.Add(time.Second)
	if result := runDeliverySweep(t, sweeper, now); result.Dispatched != 1 {
		t.Fatalf("recovery sweep = %+v, want one promotion", result)
	}
	promoted, err := st.TriggerDeliveries().Get(ctx, "WS", held.DeliveryID)
	if err != nil || promoted.Status != domain.TriggerDeliveryDispatched || promoted.Attempt != 3 {
		t.Fatalf("promoted delivery = %+v (err %v), want dispatched attempt 3", promoted, err)
	}
}
