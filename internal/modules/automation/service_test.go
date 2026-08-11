package automation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestBindingCommandsUseTypedAuthorityAndPreserveCompatibilityProjection(t *testing.T) {
	h := newTestHarness(t)
	created, err := h.service.CreateBinding(context.Background(), h.issueOperator(ActionCreateBinding), CreateBindingCommand{
		WorkspaceKey: "ws",
		Definition: BindingDefinition{
			RouteKey: "github.issue.opened", SourceKind: "github", DriverID: "driver-a",
			EventTypePatterns: []string{"github.{issue,pull_request}.opened"}, Enabled: true,
		},
	})
	if err != nil {
		t.Fatalf("CreateBinding: %v", err)
	}
	if created.BindingID != "binding-github-issue-opened" || created.DriverVersionID != "version-active" {
		t.Fatalf("created = %+v", created)
	}
	if created.RetryMaxAttempts != DefaultTriggerRetryMaxAttempts || created.RetryBackoffSeconds != DefaultTriggerRetryBackoffSeconds {
		t.Fatalf("retry defaults = %d/%d", created.RetryMaxAttempts, created.RetryBackoffSeconds)
	}
	if created.ConcurrencyPolicy != ConcurrencyOneActivePerEpic {
		t.Fatalf("concurrency = %q", created.ConcurrencyPolicy)
	}

	// Simulate a compatibility row pinned to a now-inactive version. An
	// unrelated edit retains that projection; only admission selects active.
	key := bindingMapKey("ws", created.BindingID)
	h.persistence.bindings[key].DriverVersionID = "version-inactive"
	callsBefore := len(h.catalog.calls)
	name := "renamed"
	updated, err := h.service.UpdateBinding(context.Background(), h.issueOperator(ActionUpdateBinding), UpdateBindingCommand{
		WorkspaceKey: "ws", BindingID: created.BindingID, Patch: BindingPatch{Name: &name},
	})
	if err != nil {
		t.Fatalf("UpdateBinding: %v", err)
	}
	if updated.Name != name || updated.DriverVersionID != "version-inactive" {
		t.Fatalf("updated = %+v", updated)
	}
	if len(h.catalog.calls) != callsBefore {
		t.Fatalf("unrelated update resolved catalog: calls %d -> %d", callsBefore, len(h.catalog.calls))
	}

	_, err = h.service.CreateBinding(context.Background(), h.issueOperator(ActionUpdateBinding), CreateBindingCommand{
		WorkspaceKey: "ws", Definition: BindingDefinition{BindingID: "denied", SourceKind: "github", RouteKey: "x", DriverID: "driver-a"},
	})
	assertErrorIs(t, err, authority.ErrAdmissionDenied)
	if _, exists := h.persistence.bindings[bindingMapKey("ws", "denied")]; exists {
		t.Fatal("wrong-action authority reached persistence")
	}
}

func TestBindingTargetChangesResolveOnlyActivatedVersion(t *testing.T) {
	h := newTestHarness(t)
	h.catalog.values["driver-b"] = effectiveVersion("ws", "driver-b", "version-b-active")
	h.persistence.seedBinding(seedBinding("binding-a", "github.issue.opened"))

	driverB := "driver-b"
	updated, err := h.service.UpdateBinding(context.Background(), h.issueOperator(ActionUpdateBinding), UpdateBindingCommand{
		WorkspaceKey: "ws", BindingID: "binding-a", Patch: BindingPatch{DriverID: &driverB},
	})
	if err != nil {
		t.Fatalf("UpdateBinding driver: %v", err)
	}
	if updated.DriverID != "driver-b" || updated.DriverVersionID != "version-b-active" {
		t.Fatalf("updated target = %s/%s", updated.DriverID, updated.DriverVersionID)
	}

	inactive := "version-b-inactive"
	_, err = h.service.UpdateBinding(context.Background(), h.issueOperator(ActionUpdateBinding), UpdateBindingCommand{
		WorkspaceKey: "ws", BindingID: "binding-a", Patch: BindingPatch{DriverVersionID: &inactive},
	})
	assertErrorIs(t, err, ErrConflict)
}

func TestBindingCommandsOwnCronSchedulePolicy(t *testing.T) {
	tests := []struct {
		name       string
		sourceKind string
		schedule   string
		timezone   string
		valid      bool
	}{
		{name: "standard five field", sourceKind: SourceKindCron, schedule: "15 2 * * 1-5", timezone: "America/Los_Angeles", valid: true},
		{name: "descriptor", sourceKind: SourceKindCron, schedule: "@hourly", timezone: "UTC", valid: true},
		{name: "missing", sourceKind: SourceKindCron},
		{name: "six field", sourceKind: SourceKindCron, schedule: "0 15 2 * * 1-5"},
		{name: "invalid timezone", sourceKind: SourceKindCron, schedule: "15 2 * * *", timezone: "Mars/Olympus"},
		{name: "non cron schedule", sourceKind: "github", schedule: "@hourly"},
		{name: "non cron timezone", sourceKind: "github", timezone: "UTC"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newTestHarness(t)
			created, err := h.service.CreateBinding(t.Context(), h.issueOperator(ActionCreateBinding), CreateBindingCommand{
				WorkspaceKey: "ws", Definition: BindingDefinition{
					BindingID: "scheduled", RouteKey: "scheduled.route", SourceKind: test.sourceKind,
					DriverID: "driver-a", Schedule: test.schedule, ScheduleTimezone: test.timezone,
				},
			})
			if test.valid {
				if err != nil || created.Schedule != test.schedule || created.ScheduleTimezone != test.timezone {
					t.Fatalf("CreateBinding = %+v, %v", created, err)
				}
				return
			}
			assertErrorIs(t, err, ErrInvalid)
			if h.persistence.bindings[bindingMapKey("ws", "scheduled")] != nil {
				t.Fatal("invalid schedule reached persistence")
			}
		})
	}

	h := newTestHarness(t)
	binding := seedBinding("cron-update", "cron:cron-update")
	binding.SourceKind, binding.Schedule, binding.ScheduleTimezone = SourceKindCron, "@daily", "UTC"
	h.persistence.seedBinding(binding)
	bad := "61 * * * *"
	_, err := h.service.UpdateBinding(t.Context(), h.issueOperator(ActionUpdateBinding), UpdateBindingCommand{
		WorkspaceKey: "ws", BindingID: binding.BindingID, Patch: BindingPatch{Schedule: &bad},
	})
	assertErrorIs(t, err, ErrInvalid)
	if got := h.persistence.bindings[bindingMapKey("ws", binding.BindingID)].Schedule; got != "@daily" {
		t.Fatalf("invalid update persisted schedule %q", got)
	}
}

func TestBindingDisableBeforeDeleteAndManagedGuard(t *testing.T) {
	h := newTestHarness(t)
	h.persistence.seedBinding(seedBinding("binding-a", "github.issue.opened"))

	err := h.service.DeleteBinding(context.Background(), h.issueOperator(ActionDeleteBinding), BindingCommand{WorkspaceKey: "ws", BindingID: "binding-a"})
	assertErrorIs(t, err, ErrBindingEnabled)
	if h.persistence.deleteCalls != 0 {
		t.Fatal("enabled binding was deleted")
	}

	disabled, err := h.service.DisableBinding(context.Background(), h.issueOperator(ActionDisableBinding), BindingCommand{WorkspaceKey: "ws", BindingID: "binding-a"})
	if err != nil || disabled.Enabled {
		t.Fatalf("DisableBinding = %+v, %v", disabled, err)
	}
	if err := h.service.DeleteBinding(context.Background(), h.issueOperator(ActionDeleteBinding), BindingCommand{WorkspaceKey: "ws", BindingID: "binding-a"}); err != nil {
		t.Fatalf("DeleteBinding: %v", err)
	}
	if h.persistence.deleteCalls != 1 {
		t.Fatalf("delete calls = %d", h.persistence.deleteCalls)
	}

	managed := seedBinding("managed", "github.managed")
	managed.Enabled = false
	managed.TargetAgentServiceID = "agent-1"
	h.persistence.seedBinding(managed)
	err = h.service.DeleteBinding(context.Background(), h.issueOperator(ActionDeleteBinding), BindingCommand{WorkspaceKey: "ws", BindingID: "managed"})
	assertErrorIs(t, err, ErrManagedBinding)
}

func TestOrdinaryBindingCommandsRejectManagedOwnershipForgery(t *testing.T) {
	h := newTestHarness(t)
	_, err := h.service.CreateBinding(t.Context(), h.issueOperator(ActionCreateBinding), CreateBindingCommand{
		WorkspaceKey: "ws",
		Definition: BindingDefinition{
			BindingID: "forged-create", SourceKind: "github", RouteKey: "github.forged.create", DriverID: "driver-a",
			TargetAgentServiceID: "agent-1",
		},
	})
	assertErrorIs(t, err, ErrManagedBinding)
	if h.persistence.bindings[bindingMapKey("ws", "forged-create")] != nil {
		t.Fatal("ordinary create persisted managed ownership")
	}

	h.persistence.seedBinding(seedBinding("ordinary", "github.ordinary"))
	for _, target := range []string{"", "agent-1"} {
		target := target
		t.Run("update target "+target, func(t *testing.T) {
			_, err := h.service.UpdateBinding(t.Context(), h.issueOperator(ActionUpdateBinding), UpdateBindingCommand{
				WorkspaceKey: "ws", BindingID: "ordinary", Patch: BindingPatch{TargetAgentServiceID: &target},
			})
			assertErrorIs(t, err, ErrManagedBinding)
		})
	}
	if h.persistence.unmanagedReplaceCalls != 0 {
		t.Fatalf("forged updates reached persistence %d times", h.persistence.unmanagedReplaceCalls)
	}
}

func TestOrdinaryBindingConditionalMutationsRejectManagedConversionAndRecreation(t *testing.T) {
	type operation struct {
		name    string
		enabled bool
		invoke  func(*testHarness, string) error
	}
	operations := []operation{
		{
			name: "update", enabled: true,
			invoke: func(h *testHarness, bindingID string) error {
				name := "stale ordinary update"
				_, err := h.service.UpdateBinding(t.Context(), h.issueOperator(ActionUpdateBinding), UpdateBindingCommand{
					WorkspaceKey: "ws", BindingID: bindingID, Patch: BindingPatch{Name: &name},
				})
				return err
			},
		},
		{
			name: "enable", enabled: false,
			invoke: func(h *testHarness, bindingID string) error {
				_, err := h.service.EnableBinding(t.Context(), h.issueOperator(ActionEnableBinding), BindingCommand{WorkspaceKey: "ws", BindingID: bindingID})
				return err
			},
		},
		{
			name: "disable", enabled: true,
			invoke: func(h *testHarness, bindingID string) error {
				_, err := h.service.DisableBinding(t.Context(), h.issueOperator(ActionDisableBinding), BindingCommand{WorkspaceKey: "ws", BindingID: bindingID})
				return err
			},
		},
		{
			name: "delete", enabled: false,
			invoke: func(h *testHarness, bindingID string) error {
				return h.service.DeleteBinding(t.Context(), h.issueOperator(ActionDeleteBinding), BindingCommand{WorkspaceKey: "ws", BindingID: bindingID})
			},
		},
	}

	for _, operation := range operations {
		operation := operation
		for _, race := range []string{"convert", "recreate"} {
			race := race
			t.Run(operation.name+"/"+race, func(t *testing.T) {
				h := newTestHarness(t)
				bindingID := "ordinary-race"
				created, err := h.service.CreateBinding(t.Context(), h.issueOperator(ActionCreateBinding), CreateBindingCommand{
					WorkspaceKey: "ws",
					Definition: BindingDefinition{
						BindingID: bindingID, Name: "original ordinary", SourceKind: "github", RouteKey: "github.ordinary.race",
						DriverID: "driver-a", Enabled: operation.enabled,
					},
				})
				if err != nil {
					t.Fatalf("CreateBinding: %v", err)
				}
				h.persistence.managedMutationHook = func(p *fakePersistence) {
					p.mu.Lock()
					defer p.mu.Unlock()
					key := bindingMapKey("ws", bindingID)
					current := cloneBinding(p.bindings[key])
					current.TargetAgentServiceID = "agent-1"
					current.Name = "concurrent managed " + race
					current.UpdatedAt = current.UpdatedAt.Add(time.Microsecond)
					if race == "recreate" {
						current.CreatedAt = created.CreatedAt.Add(time.Second)
						current.UpdatedAt = current.CreatedAt
					}
					p.bindings[key] = current
				}

				err = operation.invoke(h, bindingID)
				assertErrorIs(t, err, ErrManagedBinding)
				current := h.persistence.bindings[bindingMapKey("ws", bindingID)]
				if current == nil || current.TargetAgentServiceID != "agent-1" || current.Name != "concurrent managed "+race {
					t.Fatalf("stale ordinary %s mutated managed %s row: %+v", operation.name, race, current)
				}
				if race == "recreate" && current.CreatedAt.Equal(created.CreatedAt) {
					t.Fatalf("recreated generation identity was lost: %+v", current)
				}
			})
		}
	}
}

func TestWebhookAdmissionMatchesDeterministicallyFiltersActorsAndSnapshotsActiveTarget(t *testing.T) {
	h := newTestHarness(t)
	exact := seedBinding("z-exact", "github.issue.opened")
	exact.TargetAgentServiceID = "agent-service-exact"
	patternB := seedBinding("b-pattern", "other", "github.*.*")
	patternA := seedBinding("a-pattern", "other-a", "github.{issue,pull_request}.opened")
	filtered := seedBinding("c-filtered", "other-c", "github.*.*")
	filtered.ActorFilter = &ActorFilter{ExcludeActorKinds: []string{"external"}, AllowActors: []string{"octocat"}}
	for _, binding := range []*Binding{patternB, exact, filtered, patternA} {
		h.persistence.seedBinding(binding)
	}

	payload := []byte(`{"issue":7}`)
	attrs := map[string]string{"repo": "loom"}
	result, err := h.service.AdmitEvent(context.Background(), NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent)), AdmitEventCommand{
		WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
		SourceEventID: "delivery-7", EventType: "issue.opened", SubjectRef: "issue-7", ActorRef: "octocat",
		Payload: payload, SubjectAttrs: attrs,
	})
	if err != nil {
		t.Fatalf("AdmitEvent: %v", err)
	}
	if result.Event.Origin != EventOriginExternal || result.Event.HopDepth != 0 || result.Event.IdempotencyKey != "github:delivery-7" {
		t.Fatalf("event provenance = %+v", result.Event)
	}
	if result.EventType != "issue.opened" || result.RouteKey != "github.issue.opened" {
		t.Fatalf("admission response route = %q/%q", result.EventType, result.RouteKey)
	}
	wantDigestBytes := sha256.Sum256(payload)
	wantDigest := "sha256:" + hex.EncodeToString(wantDigestBytes[:])
	if result.Event.RawPayloadDigest != wantDigest {
		t.Fatalf("digest = %q, want %q", result.Event.RawPayloadDigest, wantDigest)
	}
	wantOrder := []string{"z-exact", "a-pattern", "b-pattern", "c-filtered"}
	gotOrder := make([]string, len(result.Deliveries))
	for index, delivery := range result.Deliveries {
		gotOrder[index] = delivery.TriggerBindingID
	}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("delivery order = %v, want %v", gotOrder, wantOrder)
	}
	if got := callBindingIDs(h.execution.calls); !reflect.DeepEqual(got, []string{"z-exact", "a-pattern", "b-pattern"}) {
		t.Fatalf("dispatch order = %v", got)
	}
	if h.execution.calls[0].DeliveryID == "" || h.execution.calls[0].ExpectedDeliveryStatus != DeliveryAccepted ||
		h.execution.calls[0].ExpectedDeliveryAttempt != 1 || h.execution.calls[0].TargetAgentServiceID != "agent-service-exact" {
		t.Fatalf("reserved dispatch locator/snapshot = %+v", h.execution.calls[0])
	}
	filteredDelivery := findDelivery(t, result, "c-filtered")
	if filteredDelivery.Status != DeliveryRejected || filteredDelivery.RejectionReason != RejectionReasonActorFilter {
		t.Fatalf("filtered delivery = %+v", filteredDelivery)
	}
	for _, id := range []string{"z-exact", "a-pattern", "b-pattern"} {
		delivery := findDelivery(t, result, id)
		if delivery.DriverVersionID != "version-active" || delivery.Attempt != 1 || delivery.Status != DeliveryDispatched {
			t.Fatalf("delivery %s = %+v", id, delivery)
		}
	}
	if len(h.catalog.calls) != 1 {
		t.Fatalf("same-driver catalog resolutions = %d, want 1", len(h.catalog.calls))
	}
	if !reflect.DeepEqual(h.persistence.lastReservation.MatchedBindingIDs, wantOrder) || len(h.persistence.lastReservation.CatalogGuards) != 3 {
		t.Fatalf("reservation guards = %+v", h.persistence.lastReservation)
	}
	if h.persistence.lastReservation.BindingSetRevision == 0 {
		t.Fatal("binding-set revision was not carried into reservation")
	}
	guard := h.persistence.lastReservation.CatalogGuards[0]
	if guard.DriverRevision != 7 || guard.VersionID != "version-active" || guard.SourceDigest == "" || guard.BundleDigest == "" {
		t.Fatalf("catalog guard = %+v", guard)
	}

	payload[0] = 'X'
	attrs["repo"] = "mutated"
	if string(h.execution.calls[0].Payload) != `{"issue":7}` || h.execution.calls[0].SubjectAttrs["repo"] != "loom" {
		t.Fatal("payload or attributes were not defensively copied")
	}
}

func TestDeliveryDispatchIdempotencyKeyPreservesLegacyAndBoundsFallback(t *testing.T) {
	bindingID := "binding-a"
	if got := DeliveryDispatchIdempotencyKey("", bindingID); got != "" {
		t.Fatalf("missing event identity produced dispatch key %q", got)
	}
	if got := DeliveryDispatchIdempotencyKey("event-a", ""); got != "" {
		t.Fatalf("missing binding identity produced dispatch key %q", got)
	}
	boundaryEventKey := strings.Repeat("e", maxDeliveryDispatchIdempotencyKeyLen-len(bindingID)-1)
	boundaryLegacy := boundaryEventKey + "#" + bindingID
	if len(boundaryLegacy) != maxDeliveryDispatchIdempotencyKeyLen {
		t.Fatalf("boundary fixture length = %d", len(boundaryLegacy))
	}
	if got := DeliveryDispatchIdempotencyKey(boundaryEventKey, bindingID); got != boundaryLegacy {
		t.Fatalf("128-byte legacy key changed to %q", got)
	}

	oversizedEventKey := boundaryEventKey + "x"
	legacy := oversizedEventKey + "#" + bindingID
	wantDigest := sha256.Sum256([]byte(legacy))
	want := deliveryDispatchHashPrefix + hex.EncodeToString(wantDigest[:])
	got := DeliveryDispatchIdempotencyKey(oversizedEventKey, bindingID)
	if got != want || len(got) > maxDeliveryDispatchIdempotencyKeyLen || got != strings.TrimSpace(got) {
		t.Fatalf("oversized key = %q (%d), want %q", got, len(got), want)
	}
	if replay := DeliveryDispatchIdempotencyKey(oversizedEventKey, bindingID); replay != got {
		t.Fatalf("oversized key is not deterministic: %q != %q", replay, got)
	}
	if distinct := DeliveryDispatchIdempotencyKey(oversizedEventKey+"y", bindingID); distinct == got {
		t.Fatalf("distinct oversized legacy inputs collided at %q", got)
	}
	liveEventKey := internalEventIdempotencyKey("ws", "task-ready-reconcile-v1-"+strings.Repeat("a", 64))
	liveBindingID := "agent-binding-" + strings.Repeat("b", 32)
	if live := DeliveryDispatchIdempotencyKey(liveEventKey, liveBindingID); !strings.HasPrefix(live, deliveryDispatchHashPrefix) || len(live) > maxDeliveryDispatchIdempotencyKeyLen {
		t.Fatalf("task-ready reconcile dispatch key = %q (%d)", live, len(live))
	}
	if noncanonical := DeliveryDispatchIdempotencyKey(" "+boundaryEventKey[:8], bindingID); !strings.HasPrefix(noncanonical, deliveryDispatchHashPrefix) {
		t.Fatalf("non-canonical legacy key was not bounded: %q", noncanonical)
	}
	for name, eventKey := range map[string]string{
		"embedded space": "event key",
		"control byte":   "event\x00key",
		"unicode":        "événement",
	} {
		t.Run(name, func(t *testing.T) {
			key := DeliveryDispatchIdempotencyKey(eventKey, bindingID)
			if !strings.HasPrefix(key, deliveryDispatchHashPrefix) || !deliveryDispatchLegacyKeyAccepted(key) {
				t.Fatalf("fallback key = %q", key)
			}
		})
	}
}

func TestInternalEventIdempotencyKeyPreservesValidLegacyAndBoundsMaxWorkspace(t *testing.T) {
	workspace := strings.Repeat("W", 32)
	boundarySourceEventID := strings.Repeat("e", maxDeliveryDispatchIdempotencyKeyLen-len("internal:")-len(workspace)-1)
	boundaryLegacy := "internal:" + workspace + ":" + boundarySourceEventID
	if len(boundaryLegacy) != maxDeliveryDispatchIdempotencyKeyLen {
		t.Fatalf("boundary internal key length = %d", len(boundaryLegacy))
	}
	if got := internalEventIdempotencyKey(workspace, boundarySourceEventID); got != boundaryLegacy {
		t.Fatalf("valid 128-byte internal key changed to %q", got)
	}

	reconcileSourceEventID := "task-ready-reconcile-v1-" + strings.Repeat("a", 64)
	legacy := "internal:" + workspace + ":" + reconcileSourceEventID
	if len(legacy) <= maxDeliveryDispatchIdempotencyKeyLen {
		t.Fatalf("max-workspace fixture unexpectedly fits: %d", len(legacy))
	}
	wantDigest := sha256.Sum256([]byte(legacy))
	want := internalAdmissionHashPrefix + hex.EncodeToString(wantDigest[:])
	got := internalEventIdempotencyKey(workspace, reconcileSourceEventID)
	if got != want || !deliveryDispatchLegacyKeyAccepted(got) {
		t.Fatalf("bounded internal key = %q (%d), want %q", got, len(got), want)
	}
	if replay := internalEventIdempotencyKey(workspace, reconcileSourceEventID); replay != got {
		t.Fatalf("bounded internal key is not deterministic: %q != %q", replay, got)
	}
	if distinct := internalEventIdempotencyKey(workspace, reconcileSourceEventID+"x"); distinct == got {
		t.Fatalf("distinct oversized internal inputs collided at %q", got)
	}
}

func TestTaskReadyRepositoryRequirementDeduplicatesOnlyPromptAgentFanout(t *testing.T) {
	seedPromptAgentBindings := func(h *testHarness) {
		h.t.Helper()
		h.catalog.values[promptAgentDriverID] = effectiveVersion("ws", promptAgentDriverID, "prompt-version")
		for _, binding := range []*Binding{
			{
				WorkspaceKey: "ws", BindingID: "prompt-z", Name: "coder", SourceKind: SourceKindInternal,
				SourceConfigRef: "coder", RouteKey: "internal:prompt-z", EventTypePatterns: []string{taskReadyRouteKey},
				DriverID: promptAgentDriverID, DriverVersionID: "old-prompt-version", TargetAgentServiceID: "agent-coder",
				ConcurrencyPolicy: ConcurrencyOneActivePerEpic, RetryMaxAttempts: 5, RetryBackoffSeconds: 30, Enabled: true,
			},
			{
				WorkspaceKey: "ws", BindingID: "prompt-a", Name: "planner", SourceKind: SourceKindInternal,
				SourceConfigRef: "planner", RouteKey: "internal:prompt-a", EventTypePatterns: []string{taskReadyRouteKey},
				DriverID: promptAgentDriverID, DriverVersionID: "old-prompt-version", TargetAgentServiceID: "agent-planner",
				ConcurrencyPolicy: ConcurrencyOneActivePerEpic, RetryMaxAttempts: 5, RetryBackoffSeconds: 30, Enabled: true,
			},
		} {
			h.persistence.seedBinding(binding)
		}
	}
	admitTaskReady := func(t *testing.T, h *testHarness, sourceEventID string, payload json.RawMessage) *AdmissionResult {
		t.Helper()
		result, err := h.service.AdmitEvent(t.Context(), NewSystemEventAuthority(h.issueSystem(ActionAdmitEvent)), AdmitEventCommand{
			WorkspaceKey: "ws", SourceKind: SourceKindInternal, SourceEventID: sourceEventID,
			EventType: taskReadyEventType, SubjectRef: "issue:TASK-1", Payload: payload,
		})
		if err != nil {
			t.Fatalf("AdmitEvent: %v", err)
		}
		return result
	}
	exhaustTaskReadyGeneration := func(h *testHarness, sourceEventID string, result *AdmissionResult) {
		h.t.Helper()
		h.persistence.mu.Lock()
		defer h.persistence.mu.Unlock()
		record := h.persistence.reservations[reservationMapKey("ws", internalEventIdempotencyKey("ws", sourceEventID))]
		if record == nil || record.result == nil || len(record.result.Deliveries) != len(result.Deliveries) {
			h.t.Fatalf("reservation for %q = %#v", sourceEventID, record)
		}
		for index, delivery := range result.Deliveries {
			receipt := cloneDelivery(delivery)
			receipt.Status, receipt.RejectionReason, receipt.DriverRunID = DeliveryAccepted, "", ""
			receipt.Attempt, receipt.NextRetryAt, receipt.ErrorClass = 1, nil, ""
			receipt.UpdatedAt = receipt.CreatedAt
			record.result.Deliveries[index].Delivery = receipt

			stored := h.persistence.deliveries[deliveryMapKey("ws", delivery.DeliveryID)]
			if stored == nil {
				h.t.Fatalf("stored delivery %q is missing", delivery.DeliveryID)
			}
			stored.Status, stored.RejectionReason, stored.DriverRunID = DeliveryFailed, "", ""
			stored.Attempt, stored.NextRetryAt, stored.ErrorClass = 5, nil, DeliveryErrorRetriesExhausted
			stored.UpdatedAt = stored.CreatedAt.Add(5 * time.Second)
		}
	}
	exhaustTaskReadyLiveGeneration := func(h *testHarness, sourceEventID string, result *AdmissionResult) {
		h.t.Helper()
		h.persistence.mu.Lock()
		defer h.persistence.mu.Unlock()
		record := h.persistence.reservations[reservationMapKey("ws", internalEventIdempotencyKey("ws", sourceEventID))]
		if record == nil || record.result == nil || len(record.result.Deliveries) != len(result.Deliveries) {
			h.t.Fatalf("reservation for %q = %#v", sourceEventID, record)
		}
		for index, delivery := range result.Deliveries {
			receipt := cloneDelivery(delivery)
			receipt.Status, receipt.RejectionReason, receipt.DriverRunID = DeliveryAccepted, "", ""
			receipt.Attempt, receipt.NextRetryAt, receipt.ErrorClass = 1, nil, ""
			receipt.UpdatedAt = receipt.CreatedAt
			record.result.Deliveries[index].Delivery = receipt

			stored := h.persistence.deliveries[deliveryMapKey("ws", delivery.DeliveryID)]
			if stored == nil {
				h.t.Fatalf("stored delivery %q is missing", delivery.DeliveryID)
			}
			stored.RejectionReason, stored.DriverRunID, stored.NextRetryAt = "", "", nil
			switch delivery.TriggerBindingID {
			case "prompt-a":
				stored.Status, stored.Attempt, stored.ErrorClass = DeliveryFailed, 5, DeliveryErrorRetriesExhausted
				stored.UpdatedAt = stored.CreatedAt.Add(5 * time.Second)
			case "prompt-z":
				stored.Status, stored.Attempt, stored.ErrorClass = DeliveryDuplicate, 1, ""
				stored.UpdatedAt = stored.CreatedAt.Add(time.Second)
			default:
				h.t.Fatalf("unexpected prompt binding %q", delivery.TriggerBindingID)
			}
		}
	}

	t.Run("repository-required task has one stable explanatory run", func(t *testing.T) {
		h := newTestHarness(t)
		seedPromptAgentBindings(h)
		commandPayload := json.RawMessage(`{"taskId":"TASK-1","repositoryRequired":true}`)
		sourceEventID := "task-ready-reconcile-v1-" + strings.Repeat("a", 96)

		result := admitTaskReady(t, h, sourceEventID, commandPayload)
		if got := callBindingIDs(h.execution.calls); !reflect.DeepEqual(got, []string{"prompt-a"}) {
			t.Fatalf("prompt-agent DriverRuns = %v, want one deterministic winner", got)
		}
		// Automation dispatches the persisted TriggerEvent payload directly. The
		// workflow must support this flat shape; wrapping it under input.event in
		// a fixture would hide the production integration boundary.
		if got := string(h.execution.calls[0].Payload); got != string(commandPayload) {
			t.Fatalf("DriverRun payload = %s, want flat TriggerEvent payload %s", got, commandPayload)
		}
		if key := h.execution.calls[0].IdempotencyKey; !strings.HasPrefix(key, internalAdmissionHashPrefix) ||
			!deliveryDispatchLegacyKeyAccepted(key) {
			t.Fatalf("reconcile dispatch key = %q (%d)", key, len(key))
		}
		winner, duplicate := findDelivery(t, result, "prompt-a"), findDelivery(t, result, "prompt-z")
		if winner.Status != DeliveryDispatched || winner.TargetAgentServiceID != "agent-planner" {
			t.Fatalf("winner delivery = %+v", winner)
		}
		if duplicate.Status != DeliveryDuplicate || duplicate.DriverRunID != "" {
			t.Fatalf("duplicate delivery = %+v, want terminal duplicate without DriverRun", duplicate)
		}
		if len(h.persistence.transitionCalls) != 1 {
			t.Fatalf("duplicate transitions = %+v", h.persistence.transitionCalls)
		}
		transition := h.persistence.transitionCalls[0]
		if transition.DeliveryID != duplicate.DeliveryID || transition.ExpectedStatus != DeliveryAccepted ||
			transition.ExpectedAttempt != 1 || transition.Status != DeliveryDuplicate || transition.IdempotencyKey == "" {
			t.Fatalf("duplicate transition = %+v", transition)
		}

		replayed := admitTaskReady(t, h, sourceEventID, commandPayload)
		if !replayed.Replayed || len(h.execution.calls) != 1 || len(h.persistence.transitionCalls) != 1 {
			t.Fatalf("receipt replay = replayed:%v dispatches:%d transitions:%d",
				replayed.Replayed, len(h.execution.calls), len(h.persistence.transitionCalls))
		}
		if findDelivery(t, replayed, "prompt-a").Status != DeliveryDispatched ||
			findDelivery(t, replayed, "prompt-z").Status != DeliveryDuplicate {
			t.Fatalf("replayed deliveries = %+v", replayed.Deliveries)
		}
	})

	t.Run("exhausted startup generation gets one deterministic recovery generation", func(t *testing.T) {
		h := newTestHarness(t)
		seedPromptAgentBindings(h)
		payload := json.RawMessage(`{"taskId":"TASK-1","repositoryRequired":true}`)
		baseSourceEventID := "task-ready-reconcile-v1-" + strings.Repeat("c", 64)
		base := admitTaskReady(t, h, baseSourceEventID, payload)
		exhaustTaskReadyGeneration(h, baseSourceEventID, base)
		// The recovery is a genuinely new generation: it must match the current
		// binding set and Catalog instead of replaying the exhausted receipt's
		// immutable target guards.
		h.persistence.mu.Lock()
		h.persistence.bindings[bindingMapKey("ws", "prompt-a")].TargetAgentServiceID = "agent-planner-recovery"
		h.persistence.bindingSetRevision++
		h.persistence.mu.Unlock()
		h.catalog.mu.Lock()
		h.catalog.values[promptAgentDriverID] = effectiveVersion("ws", promptAgentDriverID, "prompt-recovery-version")
		h.catalog.mu.Unlock()
		h.execution.calls = nil
		h.persistence.transitionCalls = nil

		recovered := admitTaskReady(t, h, baseSourceEventID, payload)
		wantRecoveryID := taskReadyExhaustedRecoverySourceEventID(baseSourceEventID)
		if recovered.Replayed || recovered.Event == nil || recovered.Event.SourceEventID != wantRecoveryID {
			t.Fatalf("recovered result = %+v, want fresh %q", recovered, wantRecoveryID)
		}
		if len(wantRecoveryID) > maxTaskReadyRecoverySourceEventIDLen ||
			!strings.HasSuffix(wantRecoveryID, taskReadyExhaustedRecoverySuffix) {
			t.Fatalf("recovery source id = %q (%d)", wantRecoveryID, len(wantRecoveryID))
		}
		if got := callBindingIDs(h.execution.calls); !reflect.DeepEqual(got, []string{"prompt-a"}) {
			t.Fatalf("recovery DriverRuns = %v, want one deterministic owner", got)
		}
		if winner := findDelivery(t, recovered, "prompt-a"); winner.Status != DeliveryDispatched || winner.DriverRunID == "" ||
			winner.DriverVersionID != "prompt-recovery-version" || winner.TargetAgentServiceID != "agent-planner-recovery" {
			t.Fatalf("recovery winner = %+v", winner)
		}
		if sibling := findDelivery(t, recovered, "prompt-z"); sibling.Status != DeliveryDuplicate || sibling.DriverRunID != "" {
			t.Fatalf("recovery sibling = %+v", sibling)
		}
		if len(h.persistence.events) != 2 || len(h.persistence.reservations) != 2 {
			t.Fatalf("durable generations events/reservations = %d/%d, want 2/2",
				len(h.persistence.events), len(h.persistence.reservations))
		}
		if key := h.execution.calls[0].IdempotencyKey; !deliveryDispatchLegacyKeyAccepted(key) {
			t.Fatalf("recovery dispatch key = %q (%d)", key, len(key))
		}
		longBase := taskReadyReconcileSourceEventPrefix + strings.Repeat("x", 256)
		longRecovery := taskReadyExhaustedRecoverySourceEventID(longBase)
		if len(longRecovery) > maxTaskReadyRecoverySourceEventIDLen ||
			!strings.HasPrefix(longRecovery, taskReadyExhaustedRecoveryHashPrefix) ||
			!isTaskReadyExhaustedRecoverySourceEventID(longRecovery) ||
			taskReadyExhaustedRecoverySourceEventID(longBase) != longRecovery {
			t.Fatalf("bounded long recovery source id = %q (%d)", longRecovery, len(longRecovery))
		}

		dispatches, transitions := len(h.execution.calls), len(h.persistence.transitionCalls)
		again := admitTaskReady(t, h, baseSourceEventID, payload)
		if !again.Replayed || again.Event == nil || again.Event.SourceEventID != wantRecoveryID ||
			len(h.execution.calls) != dispatches || len(h.persistence.transitionCalls) != transitions ||
			len(h.persistence.events) != 2 || len(h.persistence.reservations) != 2 {
			t.Fatalf("replayed recovery = %+v calls=%d transitions=%d events=%d reservations=%d",
				again, len(h.execution.calls), len(h.persistence.transitionCalls),
				len(h.persistence.events), len(h.persistence.reservations))
		}
	})

	t.Run("live exhausted owner and duplicate sibling recover exactly once", func(t *testing.T) {
		h := newTestHarness(t)
		seedPromptAgentBindings(h)
		payload := json.RawMessage(`{"taskId":"TASK-1","repositoryRequired":true}`)
		baseSourceEventID := "task-ready-reconcile-v1-" + strings.Repeat("f", 64)
		base := admitTaskReady(t, h, baseSourceEventID, payload)
		if owner, sibling := findDelivery(t, base, "prompt-a"), findDelivery(t, base, "prompt-z"); owner.Status != DeliveryDispatched || sibling.Status != DeliveryDuplicate {
			t.Fatalf("fresh owner/sibling = %+v / %+v, want dispatched / duplicate", owner, sibling)
		}
		exhaustTaskReadyLiveGeneration(h, baseSourceEventID, base)
		h.execution.calls = nil
		h.persistence.transitionCalls = nil

		recovered := admitTaskReady(t, h, baseSourceEventID, payload)
		wantRecoveryID := taskReadyExhaustedRecoverySourceEventID(baseSourceEventID)
		if recovered.Replayed || recovered.Event == nil || recovered.Event.SourceEventID != wantRecoveryID {
			t.Fatalf("recovered result = %+v, want one fresh generation %q", recovered, wantRecoveryID)
		}
		if got := callBindingIDs(h.execution.calls); !reflect.DeepEqual(got, []string{"prompt-a"}) {
			t.Fatalf("recovery DriverRuns = %v, want one deterministic owner", got)
		}
		if owner := findDelivery(t, recovered, "prompt-a"); owner.Status != DeliveryDispatched || owner.DriverRunID == "" {
			t.Fatalf("recovery owner = %+v, want dispatched DriverRun", owner)
		}
		if sibling := findDelivery(t, recovered, "prompt-z"); sibling.Status != DeliveryDuplicate || sibling.DriverRunID != "" {
			t.Fatalf("recovery sibling = %+v, want terminal duplicate without DriverRun", sibling)
		}
		if len(h.persistence.events) != 2 || len(h.persistence.reservations) != 2 {
			t.Fatalf("durable generations events/reservations = %d/%d, want exactly 2/2",
				len(h.persistence.events), len(h.persistence.reservations))
		}

		dispatches, transitions := len(h.execution.calls), len(h.persistence.transitionCalls)
		replayed := admitTaskReady(t, h, baseSourceEventID, payload)
		if !replayed.Replayed || replayed.Event == nil || replayed.Event.SourceEventID != wantRecoveryID ||
			len(h.execution.calls) != dispatches || len(h.persistence.transitionCalls) != transitions ||
			len(h.persistence.events) != 2 || len(h.persistence.reservations) != 2 {
			t.Fatalf("stable recovery replay = %+v calls=%d transitions=%d events=%d reservations=%d",
				replayed, len(h.execution.calls), len(h.persistence.transitionCalls),
				len(h.persistence.events), len(h.persistence.reservations))
		}

		// Even if the one recovery generation later exhausts in the same live
		// owner+duplicate shape, replaying the base must not mint a third event.
		exhaustTaskReadyLiveGeneration(h, wantRecoveryID, recovered)
		replayedExhausted := admitTaskReady(t, h, baseSourceEventID, payload)
		if !replayedExhausted.Replayed || replayedExhausted.Event == nil ||
			replayedExhausted.Event.SourceEventID != wantRecoveryID ||
			len(h.execution.calls) != dispatches || len(h.persistence.transitionCalls) != transitions ||
			len(h.persistence.events) != 2 || len(h.persistence.reservations) != 2 {
			t.Fatalf("exhausted recovery replay = %+v calls=%d transitions=%d events=%d reservations=%d",
				replayedExhausted, len(h.execution.calls), len(h.persistence.transitionCalls),
				len(h.persistence.events), len(h.persistence.reservations))
		}
	})

	t.Run("exhausted recovery stays scoped to synthetic repository-required events", func(t *testing.T) {
		for _, test := range []struct {
			name          string
			sourceEventID string
			payload       json.RawMessage
		}{
			{
				name:          "ordinary internal task ready",
				sourceEventID: "ordinary-task-ready",
				payload:       json.RawMessage(`{"repositoryRequired":true}`),
			},
			{
				name:          "synthetic runnable task",
				sourceEventID: "task-ready-reconcile-v1-runnable",
				payload:       json.RawMessage(`{"repositoryRequired":false}`),
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				h := newTestHarness(t)
				seedPromptAgentBindings(h)
				first := admitTaskReady(t, h, test.sourceEventID, test.payload)
				exhaustTaskReadyGeneration(h, test.sourceEventID, first)
				h.execution.calls = nil

				replayed := admitTaskReady(t, h, test.sourceEventID, test.payload)
				if !replayed.Replayed || replayed.Event.SourceEventID != test.sourceEventID ||
					len(h.execution.calls) != 0 || len(h.persistence.events) != 1 || len(h.persistence.reservations) != 1 {
					t.Fatalf("scoped replay = %+v calls=%d events=%d reservations=%d",
						replayed, len(h.execution.calls), len(h.persistence.events), len(h.persistence.reservations))
				}
			})
		}
	})

	t.Run("run and retry owners prevent exhausted recovery", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			mutate func(*Delivery)
		}{
			{name: "driver run", mutate: func(delivery *Delivery) {
				delivery.Status, delivery.DriverRunID = DeliveryDispatched, "run-existing"
				delivery.ErrorClass, delivery.NextRetryAt = "", nil
			}},
			{name: "scheduled retry", mutate: func(delivery *Delivery) {
				next := delivery.UpdatedAt.Add(time.Minute)
				delivery.Status, delivery.DriverRunID = DeliveryFailed, ""
				delivery.ErrorClass, delivery.NextRetryAt = DeliveryErrorDispatchFailed, &next
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				h := newTestHarness(t)
				seedPromptAgentBindings(h)
				payload := json.RawMessage(`{"repositoryRequired":true}`)
				sourceEventID := "task-ready-reconcile-v1-owner-" + test.name
				first := admitTaskReady(t, h, sourceEventID, payload)
				exhaustTaskReadyGeneration(h, sourceEventID, first)
				h.persistence.mu.Lock()
				test.mutate(h.persistence.deliveries[deliveryMapKey("ws", first.Deliveries[0].DeliveryID)])
				h.persistence.mu.Unlock()
				h.execution.calls = nil

				replayed := admitTaskReady(t, h, sourceEventID, payload)
				if !replayed.Replayed || replayed.Event.SourceEventID != sourceEventID ||
					len(h.execution.calls) != 0 || len(h.persistence.events) != 1 || len(h.persistence.reservations) != 1 {
					t.Fatalf("owner replay = %+v calls=%d events=%d reservations=%d",
						replayed, len(h.execution.calls), len(h.persistence.events), len(h.persistence.reservations))
				}
			})
		}
	})

	t.Run("exhausted recovery generation cannot recurse", func(t *testing.T) {
		h := newTestHarness(t)
		seedPromptAgentBindings(h)
		payload := json.RawMessage(`{"repositoryRequired":true}`)
		sourceEventID := "task-ready-reconcile-v1-" + strings.Repeat("d", 32) + taskReadyExhaustedRecoverySuffix
		first := admitTaskReady(t, h, sourceEventID, payload)
		exhaustTaskReadyGeneration(h, sourceEventID, first)
		h.execution.calls = nil

		replayed := admitTaskReady(t, h, sourceEventID, payload)
		if !replayed.Replayed || replayed.Event.SourceEventID != sourceEventID || len(h.execution.calls) != 0 ||
			len(h.persistence.events) != 1 || len(h.persistence.reservations) != 1 {
			t.Fatalf("recursive recovery replay = %+v calls=%d events=%d reservations=%d",
				replayed, len(h.execution.calls), len(h.persistence.events), len(h.persistence.reservations))
		}
	})

	t.Run("concurrent exhausted replays converge on one recovery reservation", func(t *testing.T) {
		h := newTestHarness(t)
		h.catalog.values[promptAgentDriverID] = effectiveVersion("ws", promptAgentDriverID, "prompt-version")
		h.persistence.seedBinding(&Binding{
			WorkspaceKey: "ws", BindingID: "prompt-a", Name: "planner", SourceKind: SourceKindInternal,
			SourceConfigRef: "planner", RouteKey: "internal:prompt-a", EventTypePatterns: []string{taskReadyRouteKey},
			DriverID: promptAgentDriverID, DriverVersionID: "old-prompt-version", TargetAgentServiceID: "agent-planner",
			ConcurrencyPolicy: ConcurrencyOneActivePerEpic, RetryMaxAttempts: 5, RetryBackoffSeconds: 30, Enabled: true,
		})
		idempotentExecution := &idempotentTestExecution{delegate: h.execution}
		h.service.execution = idempotentExecution
		payload := json.RawMessage(`{"repositoryRequired":true}`)
		sourceEventID := "task-ready-reconcile-v1-" + strings.Repeat("e", 64)
		first := admitTaskReady(t, h, sourceEventID, payload)
		exhaustTaskReadyGeneration(h, sourceEventID, first)

		ctx := t.Context()
		start := make(chan struct{})
		results := make([]*AdmissionResult, 2)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for index := range results {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				<-start
				results[index], errs[index] = h.service.AdmitEvent(
					ctx,
					NewSystemEventAuthority(h.issueSystem(ActionAdmitEvent)),
					AdmitEventCommand{
						WorkspaceKey: "ws", SourceKind: SourceKindInternal, SourceEventID: sourceEventID,
						EventType: taskReadyEventType, SubjectRef: "issue:TASK-1", Payload: payload,
					},
				)
			}(index)
		}
		close(start)
		wg.Wait()

		wantRecoveryID := taskReadyExhaustedRecoverySourceEventID(sourceEventID)
		for index := range results {
			if errs[index] != nil || results[index] == nil || results[index].Event == nil ||
				results[index].Event.SourceEventID != wantRecoveryID {
				t.Fatalf("concurrent result[%d] = %+v, %v", index, results[index], errs[index])
			}
		}
		if len(h.persistence.events) != 2 || len(h.persistence.reservations) != 2 ||
			idempotentExecution.callCount() != 2 {
			t.Fatalf("concurrent convergence events=%d reservations=%d dispatches=%d, want 2/2/2 including base",
				len(h.persistence.events), len(h.persistence.reservations), idempotentExecution.callCount())
		}
	})

	for _, test := range []struct {
		name       string
		status     DeliveryStatus
		driverRun  string
		errorClass string
	}{
		{name: "later dispatched run", status: DeliveryDispatched, driverRun: "run-existing"},
		{name: "later retryable failure without next retry", status: DeliveryFailed, errorClass: DeliveryErrorDispatchFailed},
	} {
		t.Run("replay prefers "+test.name+" over earlier accepted", func(t *testing.T) {
			h := newTestHarness(t)
			seedPromptAgentBindings(h)
			sourceEventID := "task-ready-current-owner-" + string(test.status)
			payload := json.RawMessage(`{"repositoryRequired":true}`)
			first := admitTaskReady(t, h, sourceEventID, payload)

			h.persistence.mu.Lock()
			record := h.persistence.reservations[reservationMapKey("ws", internalEventIdempotencyKey("ws", sourceEventID))]
			for index, delivery := range first.Deliveries {
				receipt := cloneDelivery(delivery)
				receipt.Status, receipt.RejectionReason, receipt.DriverRunID = DeliveryAccepted, "", ""
				receipt.NextRetryAt, receipt.ErrorClass, receipt.UpdatedAt = nil, "", receipt.CreatedAt
				record.result.Deliveries[index].Delivery = receipt

				stored := h.persistence.deliveries[deliveryMapKey("ws", delivery.DeliveryID)]
				stored.RejectionReason, stored.NextRetryAt = "", nil
				if delivery.TriggerBindingID == "prompt-a" {
					stored.Status, stored.DriverRunID, stored.ErrorClass = DeliveryAccepted, "", ""
				} else {
					stored.Status, stored.DriverRunID, stored.ErrorClass = test.status, test.driverRun, test.errorClass
				}
			}
			h.persistence.transitionCalls = nil
			dispatchCalls := len(h.execution.calls)
			h.persistence.mu.Unlock()

			replayed := admitTaskReady(t, h, sourceEventID, payload)
			if len(h.execution.calls) != dispatchCalls {
				t.Fatalf("existing owner replay dispatched: %d -> %d", dispatchCalls, len(h.execution.calls))
			}
			if earlier := findDelivery(t, replayed, "prompt-a"); earlier.Status != DeliveryDuplicate {
				t.Fatalf("earlier accepted delivery = %+v, want duplicate", earlier)
			}
			later := findDelivery(t, replayed, "prompt-z")
			if later.Status != test.status || later.DriverRunID != test.driverRun || later.ErrorClass != test.errorClass {
				t.Fatalf("existing owner delivery = %+v", later)
			}
			if len(h.persistence.transitionCalls) != 1 || h.persistence.transitionCalls[0].Status != DeliveryDuplicate {
				t.Fatalf("owner replay transitions = %+v", h.persistence.transitionCalls)
			}
		})
	}

	t.Run("valid task preserves distinct role fanout", func(t *testing.T) {
		h := newTestHarness(t)
		seedPromptAgentBindings(h)
		result := admitTaskReady(t, h, "task-ready-valid", json.RawMessage(`{"taskId":"TASK-1","repositoryRequired":false}`))

		if got := callBindingIDs(h.execution.calls); !reflect.DeepEqual(got, []string{"prompt-a", "prompt-z"}) {
			t.Fatalf("valid task dispatches = %v, want both roles", got)
		}
		if got := []string{h.execution.calls[0].TargetAgentServiceID, h.execution.calls[1].TargetAgentServiceID}; !reflect.DeepEqual(got, []string{"agent-planner", "agent-coder"}) {
			t.Fatalf("valid task role targets = %v", got)
		}
		if len(h.persistence.transitionCalls) != 0 {
			t.Fatalf("valid task was deduplicated: %+v", h.persistence.transitionCalls)
		}
		for _, bindingID := range []string{"prompt-a", "prompt-z"} {
			if delivery := findDelivery(t, result, bindingID); delivery.Status != DeliveryDispatched {
				t.Fatalf("valid task delivery %q = %+v", bindingID, delivery)
			}
		}
	})

	t.Run("repository-required task does not suppress another driver", func(t *testing.T) {
		h := newTestHarness(t)
		seedPromptAgentBindings(h)
		other := seedBinding("other-driver", "internal:other-driver", taskReadyRouteKey)
		other.SourceKind = SourceKindInternal
		other.TargetAgentServiceID = ""
		h.persistence.seedBinding(other)

		result := admitTaskReady(t, h, "task-ready-other-driver", json.RawMessage(`{"repositoryRequired":true}`))
		if got := callBindingIDs(h.execution.calls); !reflect.DeepEqual(got, []string{"prompt-a", "other-driver"}) {
			t.Fatalf("repository-required scoped dispatches = %v", got)
		}
		if findDelivery(t, result, "other-driver").Status != DeliveryDispatched ||
			findDelivery(t, result, "prompt-a").Status != DeliveryDispatched ||
			findDelivery(t, result, "prompt-z").Status != DeliveryDuplicate {
			t.Fatalf("repository-required scoped deliveries = %+v", result.Deliveries)
		}
	})

	t.Run("actor-filtered prompt binding cannot displace the winner", func(t *testing.T) {
		h := newTestHarness(t)
		seedPromptAgentBindings(h)
		h.persistence.mu.Lock()
		h.persistence.bindings[bindingMapKey("ws", "prompt-a")].ActorFilter = &ActorFilter{ExcludeActorKinds: []string{"system"}}
		h.persistence.bindingSetRevision++
		h.persistence.mu.Unlock()

		result := admitTaskReady(t, h, "task-ready-filtered-winner", json.RawMessage(`{"repositoryRequired":true}`))
		if got := callBindingIDs(h.execution.calls); !reflect.DeepEqual(got, []string{"prompt-z"}) {
			t.Fatalf("dispatchable prompt-agent winner = %v", got)
		}
		filtered, winner := findDelivery(t, result, "prompt-a"), findDelivery(t, result, "prompt-z")
		if filtered.Status != DeliveryRejected || filtered.RejectionReason != RejectionReasonActorFilter ||
			winner.Status != DeliveryDispatched || len(h.persistence.transitionCalls) != 0 {
			t.Fatalf("filtered winner selection = filtered:%+v winner:%+v transitions:%+v",
				filtered, winner, h.persistence.transitionCalls)
		}
	})

	t.Run("terminal concurrency rejection falls through to the next role", func(t *testing.T) {
		h := newTestHarness(t)
		seedPromptAgentBindings(h)
		h.persistence.mu.Lock()
		h.persistence.bindings[bindingMapKey("ws", "prompt-a")].ConcurrencyPolicy = ConcurrencyForbid
		h.persistence.bindingSetRevision++
		h.persistence.mu.Unlock()
		h.execution.outcomes["prompt-a"] = []fakeDispatchOutcome{{
			result: &ExecutionDispatchResult{Busy: true, BusyRunID: "run-active"}, committedStatus: DeliveryRejected,
		}}

		result := admitTaskReady(t, h, "task-ready-forbid-fallback", json.RawMessage(`{"repositoryRequired":true}`))
		if got := callBindingIDs(h.execution.calls); !reflect.DeepEqual(got, []string{"prompt-a", "prompt-z"}) {
			t.Fatalf("fallback prompt-agent attempts = %v", got)
		}
		rejected, winner := findDelivery(t, result, "prompt-a"), findDelivery(t, result, "prompt-z")
		if rejected.Status != DeliveryRejected || rejected.RejectionReason != RejectionConcurrencyForbid ||
			winner.Status != DeliveryDispatched || winner.DriverRunID == "" {
			t.Fatalf("fallback winner = rejected:%+v winner:%+v", rejected, winner)
		}
	})
}

func TestAdmissionReservesRunFinishedForTrustedSystemProvenance(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		actorRef  string
	}{
		{name: "external run finished", eventType: testRunFinishedEventType, actorRef: "octocat"},
		{name: "external system actor", eventType: "pull_request", actorRef: "system"},
		{name: "external system actor namespace", eventType: "pull_request", actorRef: "system:cron"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newTestHarness(t)
			_, err := h.service.AdmitEvent(t.Context(), NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent)), AdmitEventCommand{
				WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github." + test.eventType,
				SourceEventID: testRunFinishedSourceEventIDPrefix + "child:completed",
				EventType:     test.eventType, SubjectRef: "child", ActorRef: test.actorRef,
			})
			assertErrorIs(t, err, ErrInvalid)
			if h.persistence.matchCalls != 0 || h.persistence.reserveCalls != 0 || len(h.execution.calls) != 0 {
				t.Fatalf("reserved ingress reached match/reserve/dispatch: %d/%d/%d",
					h.persistence.matchCalls, h.persistence.reserveCalls, len(h.execution.calls))
			}
		})
	}

	t.Run("workflow run finished", func(t *testing.T) {
		h := newTestHarness(t)
		h.execution.emission = &ExecutionEmissionContext{
			WorkspaceKey: "ws", RunID: "run-emitter", NodeID: "node-1",
			LeaseID: "lease-1", FencingToken: 1, ActorRef: "driver-run:run-emitter",
		}
		_, err := h.service.AdmitEvent(t.Context(), NewExecutionEventAuthority(h.issueExecution(ActionAdmitEvent)), AdmitEventCommand{
			WorkspaceKey: "ws", SourceEventID: testRunFinishedSourceEventIDPrefix + "child:completed",
			EventType: testRunFinishedEventType, SubjectRef: "child",
			ExecutionNodeID: "node-1", ExecutionLeaseID: "lease-1", ExecutionFencingToken: 1,
		})
		assertErrorIs(t, err, ErrInvalid)
		if h.persistence.matchCalls != 0 || h.persistence.reserveCalls != 0 || len(h.execution.calls) != 0 {
			t.Fatal("workflow run.finished reached match, reservation, or dispatch")
		}
	})

	t.Run("workflow ordinary event cannot occupy system actor namespace", func(t *testing.T) {
		h := newTestHarness(t)
		h.execution.emission = &ExecutionEmissionContext{
			WorkspaceKey: "ws", RunID: "run-emitter", NodeID: "node-1",
			LeaseID: "lease-1", FencingToken: 1, ActorRef: "system:cron",
		}
		_, err := h.service.AdmitEvent(t.Context(), NewExecutionEventAuthority(h.issueExecution(ActionAdmitEvent)), AdmitEventCommand{
			WorkspaceKey: "ws", SourceEventID: "event-workflow-system-actor",
			EventType: "issue.created", SubjectRef: "issue-1",
			ExecutionNodeID: "node-1", ExecutionLeaseID: "lease-1", ExecutionFencingToken: 1,
		})
		assertErrorIs(t, err, ErrInvalid)
		if h.persistence.matchCalls != 0 || h.persistence.reserveCalls != 0 || len(h.execution.calls) != 0 {
			t.Fatal("workflow reserved actor reached match, reservation, or dispatch")
		}
	})

	t.Run("genuine system run outcome", func(t *testing.T) {
		h := newTestHarness(t)
		binding := seedBinding("run-outcome", "internal.run.finished")
		binding.SourceKind = SourceKindInternal
		h.persistence.seedBinding(binding)
		principal, err := h.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
			Subject: testRunFinishedActorRef, Class: authority.ClassSystem,
			Workspace: "ws", Actions: []authority.Action{ActionAdmitEvent}, ExpiresAt: h.now.Add(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		auth, err := h.issuer.IssueSystem(principal, "ws", ActionAdmitEvent, "genuine run outcome")
		if err != nil {
			t.Fatal(err)
		}
		result, err := h.service.AdmitEvent(t.Context(), NewSystemEventAuthority(auth), AdmitEventCommand{
			WorkspaceKey: "ws", SourceKind: SourceKindInternal,
			SourceEventID: testRunFinishedSourceEventIDPrefix + "child:completed",
			EventType:     testRunFinishedEventType, SubjectRef: "child",
		})
		if err != nil || result == nil || result.Event == nil ||
			!h.eventTrustPolicy.EligibleForAdmission(
				result.Event.EventType, string(result.Event.Origin), result.Event.SourceKind,
				result.Event.ActorRef, result.Event.SourceEventID) {
			t.Fatalf("genuine run outcome admission = %+v, %v", result, err)
		}
	})
}

func TestAdmissionWithoutEventTrustPolicyFailsClosedBeforeAnySideEffect(t *testing.T) {
	h := newTestHarness(t)
	h.service.eventTrustPolicy = nil
	h.persistence.seedBinding(seedBinding("binding-a", "github.issue.opened"))

	result, err := h.service.AdmitEvent(
		t.Context(),
		NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent)),
		AdmitEventCommand{
			WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
			SourceEventID: "delivery-1", EventType: "issue.opened", SubjectRef: "issue-1",
		},
	)
	if result != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("AdmitEvent without trust policy = %+v, %v; want unavailable", result, err)
	}
	if h.persistence.matchCalls != 0 || h.persistence.reserveCalls != 0 || len(h.execution.calls) != 0 {
		t.Fatalf("missing trust policy reached match/reserve/dispatch: %d/%d/%d",
			h.persistence.matchCalls, h.persistence.reserveCalls, len(h.execution.calls))
	}
}

func TestSystemIssueJournalAdmissionClassifiesDurableRunActorsAsWorkflow(t *testing.T) {
	issueSystemAuthority := func(t *testing.T, h *testHarness, actor string) authority.SystemAuthority {
		t.Helper()
		principal, err := h.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
			Subject: actor, Class: authority.ClassSystem, Workspace: "ws",
			Actions: []authority.Action{ActionAdmitEvent}, ExpiresAt: h.now.Add(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		value, err := h.issuer.IssueSystem(principal, "ws", ActionAdmitEvent, "verified issue journal actor")
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	for _, actor := range []string{"driver-run:run-1", "task-run:task-1"} {
		t.Run(actor, func(t *testing.T) {
			h := newTestHarness(t)
			binding := seedBinding("issue-created", "internal.issue.created")
			binding.SourceKind = SourceKindInternal
			binding.ActorFilter = &ActorFilter{
				ExcludeActorKinds: []string{"workflow"},
				AllowActors:       []string{actor}, // exclusions must win
			}
			h.persistence.seedBinding(binding)

			result, err := h.service.AdmitEvent(t.Context(), NewSystemEventAuthority(issueSystemAuthority(t, h, actor)), AdmitEventCommand{
				WorkspaceKey: "ws", SourceKind: SourceKindInternal,
				SourceEventID: "fleet-journal-1", EventType: "issue.create", SubjectRef: "issue:WS-1",
			})
			if err != nil {
				t.Fatalf("AdmitEvent: %v", err)
			}
			delivery := findDelivery(t, result, binding.BindingID)
			if result.Event.Origin != EventOriginSystem || result.Event.ActorRef != actor ||
				delivery.Status != DeliveryRejected || delivery.RejectionReason != RejectionReasonActorFilter {
				t.Fatalf("journal admission = event %+v delivery %+v", result.Event, delivery)
			}
			if len(h.execution.calls) != 0 || len(h.persistence.lastReservation.CatalogGuards) != 0 {
				t.Fatalf("filtered workflow actor reached dispatch/catalog guards: calls=%d guards=%d",
					len(h.execution.calls), len(h.persistence.lastReservation.CatalogGuards))
			}
		})
	}

	t.Run("legacy unsafe binding fails closed for workflow actor", func(t *testing.T) {
		h := newTestHarness(t)
		binding := seedBinding("legacy-issue-created", "internal.issue.created")
		binding.SourceKind = SourceKindInternal
		binding.ActorFilter = nil // persisted before the create/update invariant
		h.persistence.seedBinding(binding)

		result, err := h.service.AdmitEvent(t.Context(), NewSystemEventAuthority(issueSystemAuthority(t, h, "driver-run:legacy")), AdmitEventCommand{
			WorkspaceKey: "ws", SourceKind: SourceKindInternal,
			SourceEventID: "fleet-journal-legacy", EventType: "issue.create", SubjectRef: "issue:WS-LEGACY",
		})
		if err != nil {
			t.Fatalf("AdmitEvent: %v", err)
		}
		delivery := findDelivery(t, result, binding.BindingID)
		if delivery.Status != DeliveryRejected || delivery.RejectionReason != RejectionReasonActorFilter {
			t.Fatalf("legacy unsafe delivery = %+v, want actor-filter rejection", delivery)
		}
		if len(h.execution.calls) != 0 || len(h.persistence.lastReservation.CatalogGuards) != 0 {
			t.Fatalf("legacy unsafe binding reached dispatch/catalog guards: calls=%d guards=%d",
				len(h.execution.calls), len(h.persistence.lastReservation.CatalogGuards))
		}
	})

	t.Run("human journal actor remains system", func(t *testing.T) {
		h := newTestHarness(t)
		binding := seedBinding("issue-created", "internal.issue.created")
		binding.SourceKind = SourceKindInternal
		binding.ActorFilter = &ActorFilter{ExcludeActorKinds: []string{"workflow"}}
		h.persistence.seedBinding(binding)

		result, err := h.service.AdmitEvent(t.Context(), NewSystemEventAuthority(issueSystemAuthority(t, h, "user:alice")), AdmitEventCommand{
			WorkspaceKey: "ws", SourceKind: SourceKindInternal,
			SourceEventID: "fleet-journal-human", EventType: "issue.create", SubjectRef: "issue:WS-2",
		})
		if err != nil {
			t.Fatalf("AdmitEvent: %v", err)
		}
		delivery := findDelivery(t, result, binding.BindingID)
		if delivery.Status != DeliveryDispatched || len(h.execution.calls) != 1 {
			t.Fatalf("human journal admission = delivery %+v calls=%d", delivery, len(h.execution.calls))
		}
	})
}

func TestAdmissionRejectsBindingSetEditBetweenSnapshotAndReservation(t *testing.T) {
	h := newTestHarness(t)
	h.persistence.seedBinding(seedBinding("binding-a", "github.issue.opened"))
	h.persistence.bumpRevisionAfterMatch = true
	_, err := h.service.AdmitEvent(context.Background(), NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent)), AdmitEventCommand{
		WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
		SourceEventID: "binding-race", EventType: "issue.opened",
	})
	assertErrorIs(t, err, ErrConflict)
	if len(h.execution.calls) != 0 {
		t.Fatal("stale binding snapshot reached execution")
	}
}

func TestAdmissionReplayHealsLostReservationResponseWithAdvancingClock(t *testing.T) {
	h := newTestHarness(t)
	h.persistence.seedBinding(seedBinding("binding-a", "github.issue.opened"))
	current := h.now
	h.service.now = func() time.Time { return current }
	h.persistence.commitThenError = true
	auth := NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent))
	command := AdmitEventCommand{
		WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
		SourceEventID: "delivery-retry", EventType: "issue.opened", Payload: []byte(`{"same":true}`),
	}

	if _, err := h.service.AdmitEvent(context.Background(), auth, command); err == nil {
		t.Fatal("first call should lose its committed reservation response")
	}
	if len(h.execution.calls) != 0 {
		t.Fatal("dispatch occurred without a reservation response")
	}
	current = current.Add(3 * time.Minute)
	result, err := h.service.AdmitEvent(context.Background(), auth, command)
	if err != nil {
		t.Fatalf("replayed AdmitEvent: %v", err)
	}
	if !result.Replayed || len(h.execution.calls) != 1 || result.Deliveries[0].Status != DeliveryDispatched {
		t.Fatalf("replay result = %+v, calls=%d", result, len(h.execution.calls))
	}
	if result.Deliveries[0].Attempt != 1 {
		t.Fatalf("initial dispatch advanced attempt to %d", result.Deliveries[0].Attempt)
	}
	if len(h.persistence.transitionCalls) != 0 {
		t.Fatalf("committed dispatch was transitioned again: %+v", h.persistence.transitionCalls)
	}
	dispatch := h.execution.calls[0]
	if dispatch.ExpectedDeliveryStatus != DeliveryAccepted || dispatch.ExpectedDeliveryAttempt != 1 {
		t.Fatalf("dispatch CAS = %+v", dispatch)
	}

	current = current.Add(3 * time.Minute)
	result, err = h.service.AdmitEvent(context.Background(), auth, command)
	if err != nil || !result.Replayed {
		t.Fatalf("settled replay = %+v, %v", result, err)
	}
	if len(h.execution.calls) != 1 {
		t.Fatalf("settled replay redispatched: %d calls", len(h.execution.calls))
	}
}

func TestAdmissionReplayRefreshesCurrentDeliveryProgress(t *testing.T) {
	t.Run("failed current delivery stays in retry lane", func(t *testing.T) {
		h := newTestHarness(t)
		h.persistence.seedBinding(seedBinding("binding-a", "github.issue.opened"))
		h.execution.outcomes["binding-a"] = []fakeDispatchOutcome{{err: errors.New("initial dispatch rejected")}}
		auth := NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent))
		command := AdmitEventCommand{
			WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
			SourceEventID: "failed-current-replay", EventType: "issue.opened",
		}

		first, err := h.service.AdmitEvent(t.Context(), auth, command)
		if err == nil || first == nil || first.Deliveries[0].Status != DeliveryFailed {
			t.Fatalf("initial failed admission = %+v, %v", first, err)
		}
		// Fleet replays the immutable admission receipt, not the later Delivery
		// progress. Detach the fake's receipt from its mutable storage row to
		// model that production behavior exactly.
		h.persistence.mu.Lock()
		record := h.persistence.reservations[reservationMapKey("ws", "github:failed-current-replay")]
		receipt := cloneDelivery(first.Deliveries[0])
		receipt.Status, receipt.RejectionReason, receipt.DriverRunID = DeliveryAccepted, "", ""
		receipt.NextRetryAt, receipt.ErrorClass, receipt.UpdatedAt = nil, "", receipt.CreatedAt
		record.result.Deliveries[0].Delivery = receipt
		h.persistence.mu.Unlock()

		replayed, err := h.service.AdmitEvent(t.Context(), auth, command)
		if err != nil || replayed == nil || !replayed.Replayed || replayed.Deliveries[0].Status != DeliveryFailed {
			t.Fatalf("refreshed replay = %+v, %v", replayed, err)
		}
		if len(h.execution.calls) != 1 || len(h.persistence.transitionCalls) != 1 {
			t.Fatalf("failed replay redispatched/retransitioned: calls=%d transitions=%d",
				len(h.execution.calls), len(h.persistence.transitionCalls))
		}
	})

	t.Run("current target identity drift fails closed", func(t *testing.T) {
		h := newTestHarness(t)
		h.persistence.seedBinding(seedBinding("binding-a", "github.issue.opened"))
		auth := NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent))
		command := AdmitEventCommand{
			WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
			SourceEventID: "current-target-drift", EventType: "issue.opened",
		}
		first, err := h.service.AdmitEvent(t.Context(), auth, command)
		if err != nil {
			t.Fatalf("first AdmitEvent: %v", err)
		}
		h.persistence.mu.Lock()
		record := h.persistence.reservations[reservationMapKey("ws", "github:current-target-drift")]
		receipt := cloneDelivery(first.Deliveries[0])
		receipt.Status, receipt.DriverRunID, receipt.UpdatedAt = DeliveryAccepted, "", receipt.CreatedAt
		record.result.Deliveries[0].Delivery = receipt
		stored := h.persistence.deliveries[deliveryMapKey("ws", first.Deliveries[0].DeliveryID)]
		stored.DriverVersionID = "corrupt-version"
		h.persistence.mu.Unlock()

		_, err = h.service.AdmitEvent(t.Context(), auth, command)
		assertErrorIs(t, err, ErrInvalidPersistedState)
		if len(h.execution.calls) != 1 {
			t.Fatalf("identity drift reached redispatch: %d", len(h.execution.calls))
		}
	})
}

func TestAdmissionReplayFirstBypassesBindingAndCatalogDriftAcrossRestart(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*testHarness)
		restart bool
	}{
		{name: "binding disabled", mutate: func(h *testHarness) {
			h.persistence.mu.Lock()
			defer h.persistence.mu.Unlock()
			h.persistence.bindings[bindingMapKey("ws", "binding-a")].Enabled = false
			h.persistence.bindingSetRevision++
		}},
		{name: "binding deleted after restart", restart: true, mutate: func(h *testHarness) {
			h.persistence.mu.Lock()
			defer h.persistence.mu.Unlock()
			delete(h.persistence.bindings, bindingMapKey("ws", "binding-a"))
			h.persistence.bindingSetRevision++
		}},
		{name: "catalog activation changed", mutate: func(h *testHarness) {
			h.catalog.mu.Lock()
			defer h.catalog.mu.Unlock()
			h.catalog.values["driver-a"] = effectiveVersion("ws", "driver-a", "version-new")
		}},
		{name: "catalog version unavailable", mutate: func(h *testHarness) {
			h.catalog.mu.Lock()
			defer h.catalog.mu.Unlock()
			delete(h.catalog.values, "driver-a")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newTestHarness(t)
			h.persistence.seedBinding(seedBinding("binding-a", "github.issue.opened"))
			auth := NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent))
			command := AdmitEventCommand{
				WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
				SourceEventID: "replay-drift", EventType: "issue.opened", Payload: []byte(`{"stable":true}`),
			}
			first, err := h.service.AdmitEvent(t.Context(), auth, command)
			if err != nil {
				t.Fatalf("first AdmitEvent: %v", err)
			}
			matchCalls, catalogCalls := h.persistence.matchCalls, len(h.catalog.calls)
			test.mutate(h)
			if test.restart {
				h.restartService()
			}
			replayed, err := h.service.AdmitEvent(t.Context(), auth, command)
			if err != nil || replayed == nil || !replayed.Replayed || replayed.Event.EventID != first.Event.EventID {
				t.Fatalf("replayed AdmitEvent = %#v, %v", replayed, err)
			}
			if h.persistence.matchCalls != matchCalls || len(h.catalog.calls) != catalogCalls {
				t.Fatalf("replay consulted mutable state: match %d->%d catalog %d->%d",
					matchCalls, h.persistence.matchCalls, catalogCalls, len(h.catalog.calls))
			}
			lastDispatch := h.execution.calls[len(h.execution.calls)-1]
			if lastDispatch.DriverVersionID != "version-active" || lastDispatch.DriverRevision != 7 ||
				lastDispatch.SourceDigest != "source-version-active" || lastDispatch.BundleDigest != "bundle-version-active" {
				t.Fatalf("replay target drifted = %#v", lastDispatch)
			}
		})
	}
}

func TestAdmissionReplayFirstPreservesFingerprintConflictAfterBindingDeletion(t *testing.T) {
	h := newTestHarness(t)
	h.persistence.seedBinding(seedBinding("binding-a", "github.issue.opened"))
	auth := NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent))
	command := AdmitEventCommand{
		WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
		SourceEventID: "replay-conflict", EventType: "issue.opened", Payload: []byte(`{"n":1}`),
	}
	if _, err := h.service.AdmitEvent(t.Context(), auth, command); err != nil {
		t.Fatalf("first AdmitEvent: %v", err)
	}
	h.persistence.mu.Lock()
	delete(h.persistence.bindings, bindingMapKey("ws", "binding-a"))
	h.persistence.bindingSetRevision++
	h.persistence.mu.Unlock()
	matchCalls, catalogCalls := h.persistence.matchCalls, len(h.catalog.calls)
	command.Payload = []byte(`{"n":2}`)
	_, err := h.service.AdmitEvent(t.Context(), auth, command)
	assertErrorIs(t, err, ErrConflict)
	if h.persistence.matchCalls != matchCalls || len(h.catalog.calls) != catalogCalls {
		t.Fatal("changed replay fingerprint reached binding or Catalog preflight")
	}
}

func TestAdmissionRejectsCorruptCommittedEventAndFreshDeliverySnapshots(t *testing.T) {
	t.Run("replayed immutable event", func(t *testing.T) {
		h := newTestHarness(t)
		h.persistence.seedBinding(seedBinding("binding-a", "github.issue.opened"))
		auth := NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent))
		command := AdmitEventCommand{
			WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
			SourceEventID: "corrupt-replay", EventType: "issue.opened", Payload: []byte(`{"stable":true}`),
		}
		if _, err := h.service.AdmitEvent(t.Context(), auth, command); err != nil {
			t.Fatalf("first AdmitEvent: %v", err)
		}
		dispatchCalls := len(h.execution.calls)
		h.persistence.mutateReserveResult = func(result *ReservationResult) {
			result.Event.EventType = "issue.deleted"
		}
		_, err := h.service.AdmitEvent(t.Context(), auth, command)
		assertErrorIs(t, err, ErrInvalidPersistedState)
		if len(h.execution.calls) != dispatchCalls {
			t.Fatal("corrupt replay reached execution")
		}
	})

	for _, test := range []struct {
		name   string
		id     string
		mutate func(*ReservationResult)
	}{
		{name: "delivery binding", id: "delivery-binding", mutate: func(result *ReservationResult) {
			result.Deliveries[0].Delivery.TriggerBindingID = "other-binding"
		}},
		{name: "Catalog guard", id: "catalog-guard", mutate: func(result *ReservationResult) {
			result.Deliveries[0].Target.DriverRevision++
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newTestHarness(t)
			h.persistence.seedBinding(seedBinding("binding-a", "github.issue.opened"))
			h.persistence.mutateReserveResult = test.mutate
			_, err := h.service.AdmitEvent(t.Context(), NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent)), AdmitEventCommand{
				WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
				SourceEventID: "corrupt-fresh-" + test.id, EventType: "issue.opened",
			})
			assertErrorIs(t, err, ErrInvalidPersistedState)
			if len(h.execution.calls) != 0 {
				t.Fatal("corrupt fresh reservation reached execution")
			}
		})
	}
}

func TestAdmissionReplayMissIsReadOnlyAndPreflightErrorsRecheckCommittedReplay(t *testing.T) {
	t.Run("miss", func(t *testing.T) {
		h := newTestHarness(t)
		_, err := h.service.AdmitEvent(t.Context(), NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent)), AdmitEventCommand{
			WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
			SourceEventID: "missing-replay", EventType: "issue.opened",
		})
		assertErrorIs(t, err, ErrNoMatchingBinding)
		if h.persistence.reserveCalls != 2 || len(h.persistence.reservations) != 0 ||
			len(h.persistence.events) != 0 || len(h.persistence.deliveries) != 0 || !h.persistence.lastReservation.ReplayOnly {
			t.Fatalf("replay miss mutated state: reservations=%d events=%d deliveries=%d calls=%d last=%#v",
				len(h.persistence.reservations), len(h.persistence.events), len(h.persistence.deliveries),
				h.persistence.reserveCalls, h.persistence.lastReservation)
		}
	})

	for _, test := range []struct {
		name        string
		preflight   func(*testHarness)
		wantMatch   int
		wantCatalog int
	}{
		{name: "binding match error", preflight: func(h *testHarness) {
			h.persistence.matchErr = errors.New("simulated concurrent binding read failure")
		}, wantMatch: 1},
		{name: "catalog unavailable", preflight: func(h *testHarness) {
			h.catalog.mu.Lock()
			delete(h.catalog.values, "driver-a")
			h.catalog.mu.Unlock()
		}, wantMatch: 1, wantCatalog: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newTestHarness(t)
			h.persistence.seedBinding(seedBinding("binding-a", "github.issue.opened"))
			auth := NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent))
			command := AdmitEventCommand{
				WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
				SourceEventID: "concurrent-replay", EventType: "issue.opened",
			}
			first, err := h.service.AdmitEvent(t.Context(), auth, command)
			if err != nil {
				t.Fatalf("first AdmitEvent: %v", err)
			}
			matches, catalogCalls := h.persistence.matchCalls, len(h.catalog.calls)
			h.persistence.replayMisses = 1
			test.preflight(h)
			replayed, err := h.service.AdmitEvent(t.Context(), auth, command)
			if err != nil || replayed == nil || !replayed.Replayed || replayed.Event.EventID != first.Event.EventID {
				t.Fatalf("rechecked replay = %#v, %v", replayed, err)
			}
			if h.persistence.matchCalls-matches != test.wantMatch || len(h.catalog.calls)-catalogCalls != test.wantCatalog {
				t.Fatalf("preflight calls match=%d catalog=%d", h.persistence.matchCalls-matches, len(h.catalog.calls)-catalogCalls)
			}
		})
	}
}

func TestAdmissionIdempotencyRejectsChangedPayload(t *testing.T) {
	h := newTestHarness(t)
	h.persistence.seedBinding(seedBinding("binding-a", "github.issue.opened"))
	auth := NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent))
	base := AdmitEventCommand{
		WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
		SourceEventID: "same-id", EventType: "issue.opened", Payload: []byte(`{"n":1}`),
	}
	if _, err := h.service.AdmitEvent(context.Background(), auth, base); err != nil {
		t.Fatalf("first AdmitEvent: %v", err)
	}
	base.Payload = []byte(`{"n":2}`)
	_, err := h.service.AdmitEvent(context.Background(), auth, base)
	assertErrorIs(t, err, ErrConflict)
}

func TestAdmissionIdempotencyRejectsChangedExplicitOccurrenceTime(t *testing.T) {
	h := newTestHarness(t)
	h.persistence.seedBinding(seedBinding("binding-a", "github.issue.opened"))
	auth := NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent))
	command := AdmitEventCommand{
		WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
		SourceEventID: "same-explicit-time-id", EventType: "issue.opened", OccurredAt: h.now.Add(-time.Hour),
	}
	if _, err := h.service.AdmitEvent(context.Background(), auth, command); err != nil {
		t.Fatalf("first AdmitEvent: %v", err)
	}
	command.OccurredAt = command.OccurredAt.Add(time.Second)
	_, err := h.service.AdmitEvent(context.Background(), auth, command)
	assertErrorIs(t, err, ErrConflict)
}

func TestWorkflowAdmissionDerivesRunActorParentHopAndIgnoresForgedFields(t *testing.T) {
	h := newTestHarness(t)
	binding := seedBinding("internal-binding", "internal.issue.created")
	binding.SourceKind = SourceKindInternal
	h.persistence.seedBinding(binding)
	h.persistence.seedEvent(&Event{
		WorkspaceKey: "ws", EventID: "parent-1", SourceKind: "github", SourceEventID: "delivery-parent",
		EventType: "issue.opened", RouteKey: "github.issue.opened", Origin: EventOriginExternal,
		HopDepth: 1, IdempotencyKey: "github:delivery-parent",
	})
	h.execution.emission = &ExecutionEmissionContext{
		WorkspaceKey: "ws", RunID: "run-trusted", ParentEventID: "parent-1",
		NodeID: "node-9", LeaseID: "lease-9", ActorRef: "agent-trusted", EpicID: "epic-trusted", FencingToken: 9,
	}
	result, err := h.service.AdmitEvent(context.Background(), NewExecutionEventAuthority(h.issueExecution(ActionAdmitEvent)), AdmitEventCommand{
		WorkspaceKey: "ws", SourceKind: "github", RouteKey: "attacker.route",
		SourceRef: "run-attacker", SourceEventID: "emission-1", EventType: "issue.create",
		ActorRef: "attacker", ParentEventID: "attacker-parent", EpicID: "epic-attacker",
		ExecutionNodeID: "node-9", ExecutionLeaseID: "lease-9", ExecutionFencingToken: 9,
	})
	if err != nil {
		t.Fatalf("AdmitEvent: %v", err)
	}
	event := result.Event
	if event.Origin != EventOriginWorkflow || event.HopDepth != 2 || event.SourceKind != SourceKindInternal ||
		event.EventType != "issue.created" || event.RouteKey != "internal.issue.created" ||
		event.ActorRef != "agent-trusted" || event.EmittingRunID != "run-trusted" ||
		event.ParentEventID != "parent-1" || event.EpicID != "epic-trusted" ||
		event.IdempotencyKey != "internal:ws:emission-1" {
		t.Fatalf("derived event = %+v", event)
	}
	delivery := findDelivery(t, result, binding.BindingID)
	if delivery.Status != DeliveryRejected || delivery.RejectionReason != RejectionReasonActorFilter {
		t.Fatalf("workflow issue delivery = %+v, want actor-filter rejection", delivery)
	}
	if len(h.execution.calls) != 0 || len(h.persistence.lastReservation.CatalogGuards) != 0 {
		t.Fatalf("workflow issue event reached dispatch/catalog guards: calls=%d guards=%d",
			len(h.execution.calls), len(h.persistence.lastReservation.CatalogGuards))
	}
}

func TestWorkflowHopCapDropsBeforeMatchingOrReservation(t *testing.T) {
	h := newTestHarness(t)
	h.persistence.seedBinding(seedBinding("unused", "internal.issue.created"))
	h.persistence.seedEvent(&Event{
		WorkspaceKey: "ws", EventID: "parent-cap", SourceKind: SourceKindInternal, SourceEventID: "parent-cap-source",
		EventType: "issue.created", RouteKey: "internal.issue.created", Origin: EventOriginSystem,
		HopDepth: DefaultEventHopDepthCap, IdempotencyKey: "internal:ws:parent-cap-source",
	})
	h.execution.emission = &ExecutionEmissionContext{
		WorkspaceKey: "ws", RunID: "run-cap", ParentEventID: "parent-cap", ActorRef: "agent",
		NodeID: "node-cap", LeaseID: "lease-cap", FencingToken: 10,
	}
	result, err := h.service.AdmitEvent(context.Background(), NewExecutionEventAuthority(h.issueExecution(ActionAdmitEvent)), AdmitEventCommand{
		WorkspaceKey: "ws", SourceEventID: "emission-cap", EventType: "issue.created",
		ExecutionNodeID: "node-cap", ExecutionLeaseID: "lease-cap", ExecutionFencingToken: 10,
	})
	if err != nil {
		t.Fatalf("AdmitEvent: %v", err)
	}
	if !result.Dropped || result.DropReason != DropReasonHopDepthExceeded || result.HopDepth != DefaultEventHopDepthCap+1 ||
		result.EventType != "issue.created" || result.RouteKey != "internal.issue.created" {
		t.Fatalf("drop result = %+v", result)
	}
	if h.persistence.reserveCalls != 0 || len(h.execution.calls) != 0 || len(h.catalog.calls) != 0 {
		t.Fatal("hop-depth drop reached matching, catalog, reservation, or dispatch")
	}
}

func TestWorkflowAdmissionRejectsMissingParentAndInvalidAuthority(t *testing.T) {
	h := newTestHarness(t)
	h.persistence.seedBinding(seedBinding("binding", "internal.issue.created"))
	h.execution.emission = &ExecutionEmissionContext{
		WorkspaceKey: "ws", RunID: "run", ParentEventID: "missing", ActorRef: "agent",
		NodeID: "node", LeaseID: "lease", FencingToken: 11,
	}
	_, err := h.service.AdmitEvent(context.Background(), NewExecutionEventAuthority(h.issueExecution(ActionAdmitEvent)), AdmitEventCommand{
		WorkspaceKey: "ws", SourceEventID: "event", EventType: "issue.created",
		ExecutionNodeID: "node", ExecutionLeaseID: "lease", ExecutionFencingToken: 11,
	})
	assertErrorIs(t, err, ErrParentEventNotFound)
	if h.persistence.reserveCalls != 0 {
		t.Fatal("missing parent reached reservation")
	}

	_, err = h.service.AdmitEvent(context.Background(), EventAuthority{}, AdmitEventCommand{
		WorkspaceKey: "ws", SourceEventID: "event", EventType: "issue.created",
	})
	assertErrorIs(t, err, authority.ErrAdmissionDenied)
}

func TestWorkflowAdmissionRejectsExecutionOwnerTupleDrift(t *testing.T) {
	h := newTestHarness(t)
	h.execution.emission = &ExecutionEmissionContext{
		WorkspaceKey: "ws", RunID: "run", NodeID: "node-current", LeaseID: "lease-current", FencingToken: 12,
	}
	base := AdmitEventCommand{
		WorkspaceKey: "ws", SourceEventID: "event", EventType: "issue.created",
		ExecutionNodeID: "node-current", ExecutionLeaseID: "lease-current", ExecutionFencingToken: 12,
	}
	tests := []struct {
		name   string
		mutate func(*AdmitEventCommand)
	}{
		{name: "node handoff", mutate: func(command *AdmitEventCommand) { command.ExecutionNodeID = "node-stale" }},
		{name: "lease handoff", mutate: func(command *AdmitEventCommand) { command.ExecutionLeaseID = "lease-stale" }},
		{name: "fence handoff", mutate: func(command *AdmitEventCommand) { command.ExecutionFencingToken-- }},
		{name: "missing tuple", mutate: func(command *AdmitEventCommand) {
			command.ExecutionNodeID, command.ExecutionLeaseID, command.ExecutionFencingToken = "", "", 0
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := base
			test.mutate(&command)
			_, err := h.service.AdmitEvent(t.Context(), NewExecutionEventAuthority(h.issueExecution(ActionAdmitEvent)), command)
			assertErrorIs(t, err, ErrConflict)
			if h.persistence.reserveCalls != 0 {
				t.Fatalf("owner tuple drift reached reservation %d times", h.persistence.reserveCalls)
			}
		})
	}
}

func TestWorkflowAdmissionReplaySurvivesOwnerHandoff(t *testing.T) {
	h := newTestHarness(t)
	binding := seedBinding("internal-binding", "internal.issue.created")
	binding.SourceKind = SourceKindInternal
	h.persistence.seedBinding(binding)
	h.execution.emission = &ExecutionEmissionContext{
		WorkspaceKey: "ws", RunID: "run-1", ActorRef: "agent-1",
		NodeID: "node-a", LeaseID: "lease-a", FencingToken: 7,
	}
	auth := NewExecutionEventAuthority(h.issueExecution(ActionAdmitEvent))
	command := AdmitEventCommand{
		WorkspaceKey: "ws", SourceEventID: "handoff-emission", EventType: "issue.created",
		ExecutionNodeID: "node-a", ExecutionLeaseID: "lease-a", ExecutionFencingToken: 7,
		Payload: []byte(`{"issue":"LOOM-1"}`),
	}
	first, err := h.service.AdmitEvent(t.Context(), auth, command)
	if err != nil || first == nil || first.Replayed {
		t.Fatalf("first AdmitEvent = %#v, %v", first, err)
	}

	h.execution.emission.NodeID = "node-b"
	h.execution.emission.LeaseID = "lease-b"
	h.execution.emission.FencingToken = 8
	replay := command
	replay.ExecutionNodeID = "node-b"
	replay.ExecutionLeaseID = "lease-b"
	replay.ExecutionFencingToken = 8
	matchCalls, catalogCalls := h.persistence.matchCalls, len(h.catalog.calls)
	replayed, err := h.service.AdmitEvent(t.Context(), auth, replay)
	if err != nil || replayed == nil || !replayed.Replayed || replayed.Event.EventID != first.Event.EventID {
		t.Fatalf("B replay after owner handoff = %#v, %v", replayed, err)
	}
	if h.persistence.matchCalls != matchCalls || len(h.catalog.calls) != catalogCalls {
		t.Fatalf("handoff replay consulted mutable preconditions: match %d->%d catalog %d->%d",
			matchCalls, h.persistence.matchCalls, catalogCalls, len(h.catalog.calls))
	}

	changed := replay
	changed.Payload = []byte(`{"issue":"LOOM-2"}`)
	_, err = h.service.AdmitEvent(t.Context(), auth, changed)
	assertErrorIs(t, err, ErrConflict)

	reserveCalls := h.persistence.reserveCalls
	staleFresh := command
	staleFresh.SourceEventID = "stale-owner-fresh-key"
	_, err = h.service.AdmitEvent(t.Context(), auth, staleFresh)
	assertErrorIs(t, err, ErrConflict)
	if h.persistence.reserveCalls != reserveCalls {
		t.Fatalf("stale A fresh event reached reservation: calls %d->%d", reserveCalls, h.persistence.reserveCalls)
	}
}

func TestAdmissionDispatchFailureContinuesOtherLegAndSchedulesRetry(t *testing.T) {
	h := newTestHarness(t)
	h.persistence.seedBinding(seedBinding("a-exact", "github.issue.opened"))
	h.persistence.seedBinding(seedBinding("b-pattern", "other", "github.*.*"))
	h.execution.outcomes["a-exact"] = []fakeDispatchOutcome{{err: errors.New("execution unavailable")}}

	result, err := h.service.AdmitEvent(context.Background(), NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent)), AdmitEventCommand{
		WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
		SourceEventID: "failure-fanout", EventType: "issue.opened",
	})
	if err == nil {
		t.Fatal("dispatch failure was not reported")
	}
	failed := findDelivery(t, result, "a-exact")
	if failed.Status != DeliveryFailed || failed.ErrorClass != DeliveryErrorDispatchFailed || failed.NextRetryAt == nil || failed.Attempt != 1 {
		t.Fatalf("failed delivery = %+v", failed)
	}
	dispatched := findDelivery(t, result, "b-pattern")
	if dispatched.Status != DeliveryDispatched || dispatched.DriverRunID == "" {
		t.Fatalf("sibling delivery = %+v", dispatched)
	}
	if got := callBindingIDs(h.execution.calls); !reflect.DeepEqual(got, []string{"a-exact", "b-pattern"}) {
		t.Fatalf("dispatch calls = %v", got)
	}
}

func TestAdmissionQueueBusyBecomesHeldWithoutAdvancingAttempt(t *testing.T) {
	h := newTestHarness(t)
	binding := seedBinding("queue", "github.issue.opened")
	binding.ConcurrencyPolicy = ConcurrencyQueue
	h.persistence.seedBinding(binding)
	h.execution.outcomes["queue"] = []fakeDispatchOutcome{{
		result: &ExecutionDispatchResult{Busy: true, BusyRunID: "run-active"}, committedStatus: DeliveryHeld,
	}}
	result, err := h.service.AdmitEvent(context.Background(), NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent)), AdmitEventCommand{
		WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
		SourceEventID: "queue-1", EventType: "issue.opened",
	})
	if err != nil {
		t.Fatalf("AdmitEvent: %v", err)
	}
	delivery := result.Deliveries[0]
	if delivery.Status != DeliveryHeld || delivery.NextRetryAt == nil || delivery.Attempt != 1 {
		t.Fatalf("held delivery = %+v", delivery)
	}
	if len(h.persistence.transitionCalls) != 0 {
		t.Fatalf("committed busy dispatch was transitioned again: %+v", h.persistence.transitionCalls)
	}
}

func TestAdmissionReplayObservesCommittedDispatchAfterLostResponse(t *testing.T) {
	h := newTestHarness(t)
	h.persistence.seedBinding(seedBinding("binding-a", "github.issue.opened"))
	h.execution.outcomes["binding-a"] = []fakeDispatchOutcome{{
		result: &ExecutionDispatchResult{RunID: "run-binding-a"}, committedStatus: DeliveryDispatched,
		err: errors.New("simulated lost dispatch response"),
	}}
	auth := NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent))
	command := AdmitEventCommand{
		WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
		SourceEventID: "lost-transition", EventType: "issue.opened",
	}
	first, err := h.service.AdmitEvent(context.Background(), auth, command)
	if err == nil || first.Deliveries[0].Status != DeliveryAccepted {
		t.Fatalf("first = %+v, %v", first, err)
	}
	if len(h.execution.calls) != 1 {
		t.Fatalf("dispatch calls = %d", len(h.execution.calls))
	}
	second, err := h.service.AdmitEvent(context.Background(), auth, command)
	if err != nil || !second.Replayed || second.Deliveries[0].Status != DeliveryDispatched {
		t.Fatalf("replay = %+v, %v", second, err)
	}
	if len(h.execution.calls) != 1 {
		t.Fatalf("lost dispatch response caused redispatch after committed replay: %d", len(h.execution.calls))
	}
}

func TestAdmissionRejectsMissingOrMismatchedCommittedDispatch(t *testing.T) {
	tests := []struct {
		name    string
		outcome fakeDispatchOutcome
	}{
		{
			name: "missing delivery",
			outcome: fakeDispatchOutcome{
				result: &ExecutionDispatchResult{RunID: "run-binding-a"},
			},
		},
		{
			name: "mismatched immutable target",
			outcome: fakeDispatchOutcome{
				result: &ExecutionDispatchResult{RunID: "run-binding-a"}, committedStatus: DeliveryDispatched,
				mutateCommitted: func(delivery *Delivery) { delivery.DriverVersionID = "wrong-version" },
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newTestHarness(t)
			h.persistence.seedBinding(seedBinding("binding-a", "github.issue.opened"))
			h.execution.outcomes["binding-a"] = []fakeDispatchOutcome{test.outcome}
			result, err := h.service.AdmitEvent(context.Background(), NewWebhookEventAuthority(h.issueWebhook(ActionAdmitEvent)), AdmitEventCommand{
				WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
				SourceEventID: "invalid-committed", EventType: "issue.opened",
			})
			assertErrorIs(t, err, ErrInvalidPersistedState)
			if result.Deliveries[0].Status != DeliveryAccepted || len(h.persistence.transitionCalls) != 0 {
				t.Fatalf("invalid committed result was accepted or transitioned: result=%+v transitions=%+v", result, h.persistence.transitionCalls)
			}
		})
	}
}

func TestManualDispatchUsesActivatedVersionAndServerDerivedActor(t *testing.T) {
	h := newTestHarness(t)
	binding := seedBinding("manual", "github.issue.opened")
	binding.SubjectKeyTemplate = "{{attrs.repo}}|{{subject_ref}}"
	binding.TargetAgentServiceID = "agent-service-manual"
	h.persistence.seedBinding(binding)
	payload := []byte(`{"manual":true}`)
	attrs := map[string]string{"repo": "loom"}
	result, err := h.service.DispatchBinding(context.Background(), h.issueOperator(ActionDispatchBinding), DispatchBindingCommand{
		WorkspaceKey: "ws", BindingID: "manual", IdempotencyKey: "manual-request-1",
		SubjectRef: "issue-9", EpicID: "epic-9", Payload: payload, SubjectAttrs: attrs,
	})
	if err != nil {
		t.Fatalf("DispatchBinding: %v", err)
	}
	if result.RunID != "run-manual" {
		t.Fatalf("result = %+v", result)
	}
	if len(h.execution.calls) != 2 || !h.execution.calls[0].ReplayOnly ||
		h.execution.calls[0].DriverVersionID != "" || h.execution.calls[0].SubjectRef != "issue-9" {
		t.Fatalf("replay probe = %+v", h.execution.calls)
	}
	call := h.execution.calls[1]
	if call.DriverVersionID != "version-active" || call.IdempotencyKey != "manual-request-1" ||
		call.DriverRevision != 7 || call.SourceDigest != "source-version-active" || call.BundleDigest != "bundle-version-active" ||
		call.ActorRef != "subject-operator" || call.SubjectKey != "loom|issue-9" || call.SourceKind != "binding-run" ||
		call.SubjectRef != "issue-9" ||
		call.DeliveryID != "" || call.ExpectedDeliveryStatus != "" || call.ExpectedDeliveryAttempt != 0 ||
		call.TargetAgentServiceID != "agent-service-manual" {
		t.Fatalf("dispatch = %+v", call)
	}
	payload[0] = 'X'
	attrs["repo"] = "changed"
	if string(call.Payload) != `{"manual":true}` || call.SubjectAttrs["repo"] != "loom" {
		t.Fatal("manual payload was not defensively copied")
	}

	_, err = h.service.DispatchBinding(context.Background(), h.issueOperator(ActionCreateBinding), DispatchBindingCommand{
		WorkspaceKey: "ws", BindingID: "manual", IdempotencyKey: "denied",
	})
	assertErrorIs(t, err, authority.ErrAdmissionDenied)
}

func TestManualDispatchReplayBypassesDeletedBindingAndCatalog(t *testing.T) {
	h := newTestHarness(t)
	snapshot := json.RawMessage(`{"workspace_key":"ws","run_id":"run-replayed","status":"queued"}`)
	h.execution.replays["deleted-binding"] = fakeDispatchOutcome{result: &ExecutionDispatchResult{
		RunID: "run-replayed", RunSnapshot: snapshot, Replayed: true,
	}}
	result, err := h.service.DispatchBinding(context.Background(), h.issueOperator(ActionDispatchBinding), DispatchBindingCommand{
		WorkspaceKey: "ws", BindingID: "deleted-binding", IdempotencyKey: "manual-replay-1",
		SubjectRef: "issue-9", Payload: json.RawMessage(`{"manual":true}`), SubjectAttrs: map[string]string{"repo": "loom"},
	})
	if err != nil || result == nil || result.RunID != "run-replayed" || !result.Replayed || string(result.RunSnapshot) != string(snapshot) {
		t.Fatalf("replayed dispatch = %#v, %v", result, err)
	}
	if len(h.execution.calls) != 1 || !h.execution.calls[0].ReplayOnly || len(h.catalog.calls) != 0 {
		t.Fatalf("replay calls execution=%+v catalog=%+v", h.execution.calls, h.catalog.calls)
	}
}

func TestQueriesReturnDefensiveCopiesAndRejectCrossWorkspaceRows(t *testing.T) {
	h := newTestHarness(t)
	binding := seedBinding("query", "github.issue.opened", "github.*.*")
	binding.Permissions = []string{"issue:read"}
	binding.ActorFilter = &ActorFilter{AllowActors: []string{"octocat"}}
	h.persistence.seedBinding(binding)
	first, err := h.service.GetBinding(context.Background(), "ws", "query")
	if err != nil {
		t.Fatalf("GetBinding: %v", err)
	}
	first.EventTypePatterns[0] = "changed"
	first.Permissions[0] = "changed"
	first.ActorFilter.AllowActors[0] = "changed"
	second, err := h.service.GetBinding(context.Background(), "ws", "query")
	if err != nil {
		t.Fatalf("GetBinding second: %v", err)
	}
	if second.EventTypePatterns[0] != "github.*.*" || second.Permissions[0] != "issue:read" || second.ActorFilter.AllowActors[0] != "octocat" {
		t.Fatal("binding query leaked mutable persistence state")
	}

	malicious := seedBinding("wrong", "github.wrong")
	h.persistence.seedBinding(malicious)
	h.persistence.bindings[bindingMapKey("ws", "wrong")].WorkspaceKey = "other"
	_, err = h.service.GetBinding(context.Background(), "ws", "wrong")
	assertErrorIs(t, err, ErrWrongWorkspace)

	event := &Event{
		WorkspaceKey: "ws", EventID: "event-query", SourceKind: "github", SourceEventID: "source-query",
		EventType: "issue.opened", RouteKey: "github.issue.opened", Origin: EventOriginExternal,
		IdempotencyKey: "github:source-query", Payload: []byte(`{"x":1}`), SubjectAttrs: map[string]string{"repo": "loom"},
	}
	h.persistence.seedEvent(event)
	queried, err := h.service.GetEvent(context.Background(), "ws", "event-query")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	queried.Payload[0] = 'X'
	queried.SubjectAttrs["repo"] = "changed"
	again, err := h.service.GetEvent(context.Background(), "ws", "event-query")
	if err != nil || string(again.Payload) != `{"x":1}` || again.SubjectAttrs["repo"] != "loom" {
		t.Fatalf("event defensive copy = %+v, %v", again, err)
	}
}
