package automation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
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

func TestBindingRejectsPlaintextSecretAtCoreBoundary(t *testing.T) {
	h := newTestHarness(t)
	binding := seedBinding("legacy-secret", "github.secret")
	binding.WebhookSecret = "plaintext"
	h.persistence.seedBinding(binding)
	_, err := h.service.GetBinding(context.Background(), "ws", "legacy-secret")
	assertErrorIs(t, err, ErrInvalidPersistedState)
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
	if got := h.execution.calls[0]; got.ActorRef != "agent-trusted" || got.EpicID != "epic-trusted" || got.SourceRef != event.EventID {
		t.Fatalf("dispatch context = %+v", got)
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
