package memstore

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// setupConcurrencyBindings seeds the shared driver fixture plus the given
// trigger bindings; unlike setupFanOutBindings it exposes the concurrency
// policy, subject key template and retry backoff the C9 scenarios exercise.
func setupConcurrencyBindings(t *testing.T, s *Store, bindings []store.TriggerBindingCreate) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "pr-review", Name: "pr-review",
		OwnerType: workflowcatalog.DriverOwnerSystem, Status: workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "v1", DriverID: "pr-review", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b", ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	for _, in := range bindings {
		in.WorkspaceKey = "WS"
		in.Name = in.BindingID
		in.SourceKind = "github"
		in.DriverID = "pr-review"
		in.DriverVersionID = "v1"
		in.TargetEntrypoint = "run"
		in.Enabled = true
		if _, err := s.TriggerBindings().Create(t.Context(), in); err != nil {
			t.Fatalf("Create trigger binding %s: %v", in.BindingID, err)
		}
	}
}

// TestDispatchTriggerRouteStampsSubjectKey mirrors fleet-db's
// TestPlatformAPITriggerRouteStampsSubjectKey: every leg renders the
// binding's subject key (template, default, or default-fallback on an
// unrenderable template) and persists it on the delivery. The memstore lane
// additionally consumes adapter-enriched SubjectAttrs ({{attrs.X}} tokens).
func TestDispatchTriggerRouteStampsSubjectKey(t *testing.T) {
	ctx := t.Context()
	s := New()
	setupConcurrencyBindings(t, s, []store.TriggerBindingCreate{
		{BindingID: "binding-default", RouteKey: "github.pr.sync"},
		{BindingID: "binding-templated", RouteKey: "lane.subject.template",
			EventTypePatterns: []string{"github.pr.*"}, SubjectKeyTemplate: "{{event_type}}:{{subject_ref}}"},
		{BindingID: "binding-unrenderable", RouteKey: "lane.subject.attrs",
			EventTypePatterns: []string{"github.pr.*"}, SubjectKeyTemplate: "{{attrs.pr_number}}"},
	})

	result, err := s.TriggerRoutes().DispatchTriggerRouteV2(ctx, "WS", "github.pr.sync", store.TriggerRouteDispatch{
		IdempotencyKey: "subject-1", EventType: "pull_request.synchronize", SubjectRef: "acme/widgets#9",
	})
	if err != nil {
		t.Fatalf("DispatchTriggerRouteV2: %v", err)
	}
	if len(result.Deliveries) != 3 {
		t.Fatalf("deliveries = %#v, want 3 legs", result.Deliveries)
	}
	wantSubjects := map[string]string{
		"binding-default":   "binding-default|acme/widgets#9",
		"binding-templated": "pull_request.synchronize:acme/widgets#9",
		// Missing attr falls back to the default key instead of failing ingest.
		"binding-unrenderable": "binding-unrenderable|acme/widgets#9",
	}
	for _, leg := range result.Deliveries {
		delivery, err := s.TriggerDeliveries().Get(ctx, "WS", leg.DeliveryID)
		if err != nil {
			t.Fatalf("Get delivery %s: %v", leg.DeliveryID, err)
		}
		if want := wantSubjects[leg.BindingID]; delivery.SubjectKey != want {
			t.Fatalf("delivery %s subject key = %q, want %q", leg.BindingID, delivery.SubjectKey, want)
		}
	}

	// Adapter-enriched attrs render {{attrs.X}} templates.
	attrsResult, err := s.TriggerRoutes().DispatchTriggerRouteV2(ctx, "WS", "github.pr.sync", store.TriggerRouteDispatch{
		IdempotencyKey: "subject-2", EventType: "pull_request.synchronize", SubjectRef: "acme/widgets#10",
		SubjectAttrs: map[string]string{"pr_number": "10"},
	})
	if err != nil {
		t.Fatalf("DispatchTriggerRouteV2 with attrs: %v", err)
	}
	for _, leg := range attrsResult.Deliveries {
		if leg.BindingID != "binding-unrenderable" {
			continue
		}
		delivery, err := s.TriggerDeliveries().Get(ctx, "WS", leg.DeliveryID)
		if err != nil || delivery.SubjectKey != "10" {
			t.Fatalf("attrs-templated delivery = %+v (err %v), want subject key 10", delivery, err)
		}
	}

	// No subject_ref: the default lane has no concurrency subject, while a
	// template still renders from the fields it references.
	noSubject, err := s.TriggerRoutes().DispatchTriggerRouteV2(ctx, "WS", "github.pr.sync", store.TriggerRouteDispatch{
		IdempotencyKey: "subject-3", EventType: "pull_request.synchronize",
	})
	if err != nil {
		t.Fatalf("DispatchTriggerRouteV2 subjectless: %v", err)
	}
	for _, leg := range noSubject.Deliveries {
		delivery, err := s.TriggerDeliveries().Get(ctx, "WS", leg.DeliveryID)
		if err != nil {
			t.Fatalf("subjectless delivery %q was not recorded: %v", leg.DeliveryID, err)
		}
		switch leg.BindingID {
		case "binding-default", "binding-unrenderable":
			if delivery.SubjectKey != "" {
				t.Fatalf("subjectless %s delivery subject key = %q, want empty", leg.BindingID, delivery.SubjectKey)
			}
		case "binding-templated":
			if delivery.SubjectKey != "pull_request.synchronize:" {
				t.Fatalf("templated subjectless delivery subject key = %q, want pull_request.synchronize:", delivery.SubjectKey)
			}
		}
	}
}

// TestDispatchTriggerRouteConcurrencyForbidRejects pins the forbid admission
// gate: while the subject has a queued/running run, a new event for the same
// subject is rejected BEFORE any run exists — delivery status rejected,
// rejection_reason concurrency_forbid, no run created. Another subject is
// unaffected, and redelivery of the rejected leg reports the recorded state.
func TestDispatchTriggerRouteConcurrencyForbidRejects(t *testing.T) {
	ctx := t.Context()
	s := New()
	setupConcurrencyBindings(t, s, []store.TriggerBindingCreate{
		{BindingID: "binding-forbid", RouteKey: "github.pr.sync",
			ConcurrencyPolicy: automation.ConcurrencyForbid},
	})
	dispatch := func(idem, subject string) *store.TriggerRouteDispatchResult {
		t.Helper()
		result, err := s.TriggerRoutes().DispatchTriggerRouteV2(ctx, "WS", "github.pr.sync", store.TriggerRouteDispatch{
			IdempotencyKey: idem, EventType: "pull_request", SubjectRef: subject,
		})
		if err != nil {
			t.Fatalf("DispatchTriggerRouteV2 %s: %v", idem, err)
		}
		return result
	}

	const subject = "acme/widgets#7"
	first := dispatch("fb-1", subject)
	if first.PrimaryRun == nil || first.Deliveries[0].Status != automation.DeliveryDispatched {
		t.Fatalf("first dispatch = %+v, want dispatched with run", first.Deliveries)
	}

	second := dispatch("fb-2", subject)
	leg := second.Deliveries[0]
	if leg.Status != automation.DeliveryRejected || leg.RejectionReason != "concurrency_forbid" || leg.RunID != "" {
		t.Fatalf("busy-subject leg = %+v, want rejected/concurrency_forbid with no run", leg)
	}
	if second.PrimaryRun != nil {
		t.Fatalf("rejected dispatch primary run = %+v, want nil", second.PrimaryRun)
	}
	rejected, err := s.TriggerDeliveries().Get(ctx, "WS", leg.DeliveryID)
	if err != nil || rejected.Status != automation.DeliveryRejected || rejected.RejectionReason != "concurrency_forbid" {
		t.Fatalf("rejected delivery = %+v (err %v), want persisted rejection", rejected, err)
	}
	if rejected.SubjectKey != "binding-forbid|"+subject || rejected.DriverRunID != "" || rejected.NextRetryAt != nil {
		t.Fatalf("rejected delivery = %+v, want subject key without run or retry", rejected)
	}
	if runs := len(s.runs.items["WS"]); runs != 1 {
		t.Fatalf("runs = %d, want 1 (forbid admits no run)", runs)
	}

	// A different subject admits normally.
	if other := dispatch("fb-3", "acme/widgets#99"); other.PrimaryRun == nil {
		t.Fatalf("other-subject dispatch = %+v, want admitted run", other.Deliveries)
	}

	// Redelivery of the rejected leg reports the recorded state unchanged.
	replay := dispatch("fb-2", subject)
	if replay.Deliveries[0] != leg {
		t.Fatalf("replayed rejected leg = %+v, want stable %+v", replay.Deliveries[0], leg)
	}
	if runs := len(s.runs.items["WS"]); runs != 2 {
		t.Fatalf("runs after replay = %d, want 2", runs)
	}
}

// TestDispatchTriggerRouteConcurrencyQueueHoldsWithNextRetryAt pins the queue
// admission gate and its sweeper-driven promotion: a busy subject suspends the
// delivery held with next_retry_at = now + binding backoff and NO run; once
// the subject frees, redelivery (the retry sweeper's re-dispatch) admits the
// run and promotes the held delivery to dispatched.
func TestDispatchTriggerRouteConcurrencyQueueHoldsWithNextRetryAt(t *testing.T) {
	ctx := t.Context()
	s := New()
	setupConcurrencyBindings(t, s, []store.TriggerBindingCreate{
		{BindingID: "binding-queue", RouteKey: "github.pr.sync",
			ConcurrencyPolicy: automation.ConcurrencyQueue, RetryBackoffSeconds: 60},
	})
	dispatch := func(idem string) *store.TriggerRouteDispatchResult {
		t.Helper()
		result, err := s.TriggerRoutes().DispatchTriggerRouteV2(ctx, "WS", "github.pr.sync", store.TriggerRouteDispatch{
			IdempotencyKey: idem, EventType: "pull_request", SubjectRef: "acme/widgets#7",
		})
		if err != nil {
			t.Fatalf("DispatchTriggerRouteV2 %s: %v", idem, err)
		}
		return result
	}

	first := dispatch("q-1")
	if first.PrimaryRun == nil {
		t.Fatalf("first dispatch = %+v, want admitted run", first.Deliveries)
	}

	before := time.Now().UTC()
	second := dispatch("q-2")
	leg := second.Deliveries[0]
	if leg.Status != automation.DeliveryHeld || leg.RunID != "" || leg.RejectionReason != "" {
		t.Fatalf("busy-subject leg = %+v, want held with no run", leg)
	}
	held, err := s.TriggerDeliveries().Get(ctx, "WS", leg.DeliveryID)
	if err != nil || held.Status != automation.DeliveryHeld || held.NextRetryAt == nil {
		t.Fatalf("held delivery = %+v (err %v), want held with next_retry_at", held, err)
	}
	if earliest := before.Add(60 * time.Second); held.NextRetryAt.Before(earliest) || held.NextRetryAt.After(earliest.Add(time.Minute)) {
		t.Fatalf("held next_retry_at = %v, want ~%v (now + 60s binding backoff)", held.NextRetryAt, earliest)
	}
	if held.SubjectKey != "binding-queue|acme/widgets#7" || held.Attempt != 1 {
		t.Fatalf("held delivery = %+v, want subject key + attempt 1", held)
	}
	if runs := len(s.runs.items["WS"]); runs != 1 {
		t.Fatalf("runs = %d, want 1 (queue admits no run while busy)", runs)
	}

	// The held delivery is due for the retry sweeper at its next_retry_at.
	due, err := s.TriggerDeliveries().ListDue(ctx, "WS", store.TriggerDeliveryDueFilter{Now: held.NextRetryAt.Add(time.Second)})
	if err != nil || len(due) != 1 || due[0].DeliveryID != held.DeliveryID {
		t.Fatalf("ListDue = %+v err=%v, want the held delivery", due, err)
	}

	// Subject frees; the sweeper's redelivery admits the run and promotes the
	// held delivery (heal path).
	s.runs.items["WS"][first.PrimaryRun.RunID].Status = domain.DriverRunCompleted
	promotedResult := dispatch("q-2")
	promotedLeg := promotedResult.Deliveries[0]
	if promotedLeg.Status != automation.DeliveryDispatched || promotedLeg.RunID == "" {
		t.Fatalf("promoted leg = %+v, want dispatched with run", promotedLeg)
	}
	promoted, err := s.TriggerDeliveries().Get(ctx, "WS", leg.DeliveryID)
	if err != nil || promoted.Status != automation.DeliveryDispatched || promoted.DriverRunID != promotedLeg.RunID {
		t.Fatalf("promoted delivery = %+v (err %v), want dispatched on the admitted run", promoted, err)
	}
	if promoted.Attempt != 2 || promoted.NextRetryAt != nil {
		t.Fatalf("promoted delivery = %+v, want attempt 2 with retry cleared", promoted)
	}
	if run, err := s.DriverRuns().Get(ctx, "WS", promotedLeg.RunID); err != nil || run.Status != domain.DriverRunQueued {
		t.Fatalf("promoted run = %+v (err %v), want queued", run, err)
	}
}

// TestDispatchTriggerRouteIdempotentRedeliveryHealsBusySubject pins the gate
// bypass: an idempotency-key hit on an existing run means the leg was already
// admitted, so redelivery while the subject is busy must heal (report the
// recorded dispatched state) instead of rejecting its own run.
func TestDispatchTriggerRouteIdempotentRedeliveryHealsBusySubject(t *testing.T) {
	ctx := t.Context()
	s := New()
	setupConcurrencyBindings(t, s, []store.TriggerBindingCreate{
		{BindingID: "binding-forbid", RouteKey: "github.pr.sync",
			ConcurrencyPolicy: automation.ConcurrencyForbid},
	})
	in := store.TriggerRouteDispatch{
		IdempotencyKey: "heal-1", EventType: "pull_request", SubjectRef: "acme/widgets#7",
	}
	first, err := s.TriggerRoutes().DispatchTriggerRouteV2(ctx, "WS", "github.pr.sync", in)
	if err != nil || first.PrimaryRun == nil {
		t.Fatalf("DispatchTriggerRouteV2 = %+v err=%v, want admitted run", first, err)
	}

	// The subject is busy with the leg's own run; same idempotency key heals.
	replay, err := s.TriggerRoutes().DispatchTriggerRouteV2(ctx, "WS", "github.pr.sync", in)
	if err != nil {
		t.Fatalf("DispatchTriggerRouteV2 redelivery: %v", err)
	}
	if replay.Deliveries[0] != first.Deliveries[0] {
		t.Fatalf("redelivered leg = %+v, want stable %+v", replay.Deliveries[0], first.Deliveries[0])
	}
	if replay.PrimaryRun == nil || replay.PrimaryRun.RunID != first.PrimaryRun.RunID {
		t.Fatalf("redelivered run = %+v, want original %s", replay.PrimaryRun, first.PrimaryRun.RunID)
	}
	if runs, deliveries := len(s.runs.items["WS"]), len(s.deliveries.items["WS"]); runs != 1 || deliveries != 1 {
		t.Fatalf("state after redelivery: runs=%d deliveries=%d, want 1/1 (no duplicates, no rejection)", runs, deliveries)
	}
}

// TestDispatchTriggerRouteConcurrentDispatchAdmitsOneRunPerSubject drives
// concurrent dispatches for one forbid-policy subject under -race: the route
// store's mutex must give the same atomicity fleet-db's Lua gate provides —
// exactly one run admitted, every other delivery rejected, no lost updates.
func TestDispatchTriggerRouteConcurrentDispatchAdmitsOneRunPerSubject(t *testing.T) {
	ctx := t.Context()
	s := New()
	setupConcurrencyBindings(t, s, []store.TriggerBindingCreate{
		{BindingID: "binding-forbid", RouteKey: "github.pr.sync",
			ConcurrencyPolicy: automation.ConcurrencyForbid},
	})

	const dispatchers = 16
	var wg sync.WaitGroup
	errs := make(chan error, dispatchers)
	for i := 0; i < dispatchers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.TriggerRoutes().DispatchTriggerRouteV2(ctx, "WS", "github.pr.sync", store.TriggerRouteDispatch{
				IdempotencyKey: fmt.Sprintf("cc-%d", i), EventType: "pull_request", SubjectRef: "acme/widgets#7",
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent DispatchTriggerRouteV2: %v", err)
	}

	if runs := len(s.runs.items["WS"]); runs != 1 {
		t.Fatalf("admitted runs = %d, want exactly 1 for the contested subject", runs)
	}
	dispatched, err := s.TriggerDeliveries().List(ctx, "WS", store.TriggerDeliveryFilter{Status: automation.DeliveryDispatched})
	if err != nil || len(dispatched) != 1 {
		t.Fatalf("dispatched deliveries = %d err=%v, want 1", len(dispatched), err)
	}
	rejected, err := s.TriggerDeliveries().List(ctx, "WS", store.TriggerDeliveryFilter{Status: automation.DeliveryRejected})
	if err != nil || len(rejected) != dispatchers-1 {
		t.Fatalf("rejected deliveries = %d err=%v, want %d", len(rejected), err, dispatchers-1)
	}
	for _, delivery := range rejected {
		if delivery.RejectionReason != "concurrency_forbid" || delivery.DriverRunID != "" {
			t.Fatalf("rejected delivery = %+v, want concurrency_forbid with no run", delivery)
		}
	}
}
