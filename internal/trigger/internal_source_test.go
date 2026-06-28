// Internal-loopback tests live in the external trigger_test package so they
// can drive the real memstore dispatch path (memstore imports trigger for the
// pattern engine, so an internal test would be an import cycle).
package trigger_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

const internalRouteKey = "internal.issue.created"

// setupInternalBinding seeds a memstore with a driver, a version and one
// trigger binding listening on the internal loopback route.
func setupInternalBinding(t *testing.T, s *memstore.Store) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "issue-bot", Name: "issue-bot",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "v1", DriverID: "issue-bot", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b", ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := s.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "b-internal", Name: "b-internal",
		SourceKind: "internal", RouteKey: internalRouteKey,
		DriverID: "issue-bot", DriverVersionID: "v1", TargetEntrypoint: "run",
		ConcurrencyPolicy: domain.TriggerBindingConcurrencyAllow, Enabled: true,
	}); err != nil {
		t.Fatalf("Create trigger binding: %v", err)
	}
}

func internalCounts(t *testing.T, s *memstore.Store) (events, runs int) {
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
	return len(evs), len(rns)
}

func TestNormalizeInternalEventType(t *testing.T) {
	tests := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		// Journal-style actions normalize to the roster naming.
		{raw: "issue.create", want: "issue.created"},
		{raw: "issue.update", want: "issue.updated"},
		{raw: "issue.close", want: "issue.closed"},
		{raw: "task.park", want: "task.parked"},
		{raw: "run.complete", want: "run.completed"},
		{raw: "run.fail", want: "run.failed"},
		{raw: "run.cancel", want: "run.cancelled"},
		{raw: "epic.run.start", want: "epic.run.started"},
		// Already-normalized and unknown verbs pass through.
		{raw: "issue.created", want: "issue.created"},
		{raw: "deploy.requested", want: "deploy.requested"},
		{raw: "issue.frobnicate", want: "issue.frobnicate"},
		// Single segment and case/space handling.
		{raw: "create", want: "created"},
		{raw: "  Issue.Create ", want: "issue.created"},
		// Invalid.
		{raw: "", wantErr: true},
		{raw: "issue create", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := trigger.NormalizeInternalEventType(tt.raw)
			if tt.wantErr {
				if !errors.Is(err, domain.ErrInvalid) {
					t.Fatalf("NormalizeInternalEventType(%q) err = %v, want ErrInvalid", tt.raw, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("NormalizeInternalEventType(%q) = %q, %v; want %q", tt.raw, got, err, tt.want)
			}
		})
	}
}

func TestInternalSourceEmitDispatchesWorkflowEvent(t *testing.T) {
	s := memstore.New()
	setupInternalBinding(t, s)
	src := &trigger.InternalSource{Store: s}

	result, err := src.Emit(t.Context(), "WS", trigger.InternalEvent{
		EventID:        "emit-1",
		EventType:      "issue.create",
		SubjectRef:     "issue#42",
		ActorRef:       "driver:run-parent",
		EmittedByRunID: "run-parent",
		Payload:        json.RawMessage(`{"issueId":"42"}`),
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if result.Dropped || result.RouteKey != internalRouteKey || result.EventType != "issue.created" ||
		result.Origin != domain.TriggerEventOriginWorkflow || result.HopDepth != 1 {
		t.Fatalf("result = %+v, want dispatched internal.issue.created workflow depth 1", result)
	}
	if result.Dispatch == nil || len(result.Dispatch.Deliveries) != 1 ||
		result.Dispatch.Deliveries[0].Status != domain.TriggerDeliveryDispatched {
		t.Fatalf("dispatch = %+v, want one dispatched delivery", result.Dispatch)
	}

	// The persisted event carries the loopback identity fields.
	event, err := s.TriggerEvents().Get(t.Context(), "WS", result.Dispatch.PrimaryRun.SourceRef)
	if err != nil {
		t.Fatalf("Get persisted event: %v", err)
	}
	if event.SourceEventID != "emit-1" || event.IdempotencyKey != "internal:WS:emit-1" ||
		event.SignatureStatus != "internal" || event.EventType != "issue.created" ||
		event.SubjectRef != "issue#42" {
		t.Fatalf("persisted event = %+v, want loopback identity fields", event)
	}

	// The admitted run's payload is the provenance envelope around the
	// emitter's payload.
	var envelope struct {
		Origin         string          `json:"origin"`
		HopDepth       int             `json:"hopDepth"`
		ParentEventID  string          `json:"parentEventId"`
		EmittedByRunID string          `json:"emittedByRunId"`
		Event          json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(result.Dispatch.PrimaryRun.Payload, &envelope); err != nil {
		t.Fatalf("decode run payload envelope: %v", err)
	}
	if envelope.Origin != "workflow" || envelope.HopDepth != 1 || envelope.ParentEventID != "" ||
		envelope.EmittedByRunID != "run-parent" || string(envelope.Event) != `{"issueId":"42"}` {
		t.Fatalf("payload envelope = %+v, want stamped workflow provenance", envelope)
	}
}

// TestInternalSourceHopDepthChainAndCap walks a re-trigger chain through the
// persisted-event parent lane (each hop's parent is the store-assigned event
// id, exactly what the driver-op derives from the admitted run's SourceRef):
// depths 1..4 dispatch, the 5th is dropped by the structural guard with an
// audit log and persists nothing.
func TestInternalSourceHopDepthChainAndCap(t *testing.T) {
	s := memstore.New()
	setupInternalBinding(t, s)
	var audit bytes.Buffer
	src := &trigger.InternalSource{Store: s, Logger: slog.New(slog.NewTextHandler(&audit, nil))}

	parentEventID := ""
	for hop := 1; hop <= trigger.DefaultInternalEventHopDepthCap; hop++ {
		result, err := src.Emit(t.Context(), "WS", trigger.InternalEvent{
			EventID:       fmt.Sprintf("chain-%d", hop),
			EventType:     "issue.created",
			SubjectRef:    fmt.Sprintf("issue#%d", hop),
			ParentEventID: parentEventID,
		})
		if err != nil {
			t.Fatalf("Emit hop %d: %v", hop, err)
		}
		if result.Dropped || result.HopDepth != hop {
			t.Fatalf("hop %d result = %+v, want dispatched at depth %d", hop, result, hop)
		}
		parentEventID = result.Dispatch.PrimaryRun.SourceRef
	}
	if audit.Len() != 0 {
		t.Fatalf("audit log before cap = %q, want empty", audit.String())
	}
	eventsBefore, runsBefore := internalCounts(t, s)

	result, err := src.Emit(t.Context(), "WS", trigger.InternalEvent{
		EventID:       "chain-over-cap",
		EventType:     "issue.created",
		SubjectRef:    "issue#5",
		ParentEventID: parentEventID,
	})
	if err != nil {
		t.Fatalf("Emit beyond cap: %v", err)
	}
	if !result.Dropped || result.DropReason != trigger.DropReasonHopDepthExceeded ||
		result.HopDepth != trigger.DefaultInternalEventHopDepthCap+1 || result.Dispatch != nil {
		t.Fatalf("beyond-cap result = %+v, want dropped %s at depth %d", result,
			trigger.DropReasonHopDepthExceeded, trigger.DefaultInternalEventHopDepthCap+1)
	}
	if eventsAfter, runsAfter := internalCounts(t, s); eventsAfter != eventsBefore || runsAfter != runsBefore {
		t.Fatalf("store after drop = %d events %d runs, want unchanged %d/%d",
			eventsAfter, runsAfter, eventsBefore, runsBefore)
	}
	for _, want := range []string{"chain-over-cap", "hop_depth=5", "drop_reason=" + trigger.DropReasonHopDepthExceeded} {
		if !strings.Contains(audit.String(), want) {
			t.Fatalf("audit log %q does not contain %q", audit.String(), want)
		}
	}
}

// TestInternalSourceParentByEmitEventID covers the ledger's direct lane: the
// caller names the parent by the original emit EventID (not the
// store-assigned id) and the chain still accumulates depth.
func TestInternalSourceParentByEmitEventID(t *testing.T) {
	s := memstore.New()
	setupInternalBinding(t, s)
	src := &trigger.InternalSource{Store: s}

	if _, err := src.Emit(t.Context(), "WS", trigger.InternalEvent{
		EventID: "root", EventType: "issue.created", SubjectRef: "issue#1",
	}); err != nil {
		t.Fatalf("Emit root: %v", err)
	}
	child, err := src.Emit(t.Context(), "WS", trigger.InternalEvent{
		EventID: "child", EventType: "issue.created", SubjectRef: "issue#2",
		ParentEventID: "root",
	})
	if err != nil {
		t.Fatalf("Emit child: %v", err)
	}
	if child.Dropped || child.HopDepth != 2 {
		t.Fatalf("child result = %+v, want depth 2 via ledger", child)
	}
}

func TestInternalSourceSystemOriginDepthZero(t *testing.T) {
	s := memstore.New()
	setupInternalBinding(t, s)
	src := &trigger.InternalSource{Store: s}

	// A parentless system emission is a scheduler root: depth 0, never capped.
	result, err := src.Emit(t.Context(), "WS", trigger.InternalEvent{
		EventID:   "sys-1",
		EventType: "issue.created",
		Origin:    domain.TriggerEventOriginSystem,
	})
	if err != nil {
		t.Fatalf("Emit system: %v", err)
	}
	if result.Dropped || result.Origin != domain.TriggerEventOriginSystem || result.HopDepth != 0 {
		t.Fatalf("system result = %+v, want dispatched system depth 0", result)
	}
}

func TestInternalSourceSystemOriginContinuesParentChain(t *testing.T) {
	s := memstore.New()
	setupInternalBinding(t, s)
	src := &trigger.InternalSource{Store: s}

	if _, err := src.Emit(t.Context(), "WS", trigger.InternalEvent{
		EventID: "root", EventType: "issue.created", SubjectRef: "issue#1",
	}); err != nil {
		t.Fatalf("Emit root: %v", err)
	}
	// A system event that names a parent (AW6 run.finished C19 stamping)
	// accumulates parent+1 instead of restarting the chain at 0.
	result, err := src.Emit(t.Context(), "WS", trigger.InternalEvent{
		EventID:       "sys-child",
		EventType:     "issue.created",
		Origin:        domain.TriggerEventOriginSystem,
		ParentEventID: "root",
	})
	if err != nil {
		t.Fatalf("Emit system child: %v", err)
	}
	if result.Dropped || result.HopDepth != 2 {
		t.Fatalf("system child = %+v, want depth 2 via ledger", result)
	}

	// And the cap applies to chained system events exactly like workflow
	// ones (same source instance, so the ledger carries the root's depth).
	capped := &trigger.InternalSource{Store: s, HopDepthCap: 1}
	if _, err := capped.Emit(t.Context(), "WS", trigger.InternalEvent{
		EventID: "capped-root", EventType: "issue.created", SubjectRef: "issue#2",
	}); err != nil {
		t.Fatalf("Emit capped root: %v", err)
	}
	result, err = capped.Emit(t.Context(), "WS", trigger.InternalEvent{
		EventID:       "sys-capped",
		EventType:     "issue.created",
		Origin:        domain.TriggerEventOriginSystem,
		ParentEventID: "capped-root",
	})
	if err != nil {
		t.Fatalf("Emit capped system child: %v", err)
	}
	if !result.Dropped || result.DropReason != trigger.DropReasonHopDepthExceeded {
		t.Fatalf("capped system child = %+v, want hop-depth drop", result)
	}
}

func TestInternalSourceEmitValidation(t *testing.T) {
	s := memstore.New()
	setupInternalBinding(t, s)
	src := &trigger.InternalSource{Store: s}

	tests := []struct {
		name string
		ws   string
		ev   trigger.InternalEvent
	}{
		{name: "missing event id", ws: "WS", ev: trigger.InternalEvent{EventType: "issue.created"}},
		{name: "missing workspace", ws: "", ev: trigger.InternalEvent{EventID: "x", EventType: "issue.created"}},
		{name: "missing event type", ws: "WS", ev: trigger.InternalEvent{EventID: "x"}},
		{name: "forged external origin", ws: "WS", ev: trigger.InternalEvent{
			EventID: "x", EventType: "issue.created", Origin: domain.TriggerEventOriginExternal,
		}},
		{name: "unknown origin", ws: "WS", ev: trigger.InternalEvent{
			EventID: "x", EventType: "issue.created", Origin: "robot",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := src.Emit(t.Context(), tt.ws, tt.ev); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("Emit err = %v, want ErrInvalid", err)
			}
		})
	}
	if events, runs := internalCounts(t, s); events != 0 || runs != 0 {
		t.Fatalf("store after rejected emits = %d events %d runs, want 0/0", events, runs)
	}
}

// TestInternalSourceIdempotentReEmit proves exactly-once across emit retries:
// the same EventID re-enters with the same loopback idempotency key and the
// dispatch path dedups the event and its fan-out leg.
func TestInternalSourceIdempotentReEmit(t *testing.T) {
	s := memstore.New()
	setupInternalBinding(t, s)
	src := &trigger.InternalSource{Store: s}

	ev := trigger.InternalEvent{EventID: "dup-1", EventType: "issue.created", SubjectRef: "issue#9"}
	first, err := src.Emit(t.Context(), "WS", ev)
	if err != nil {
		t.Fatalf("first Emit: %v", err)
	}
	second, err := src.Emit(t.Context(), "WS", ev)
	if err != nil {
		t.Fatalf("second Emit: %v", err)
	}
	if second.HopDepth != first.HopDepth {
		t.Fatalf("re-emit depth = %d, want %d", second.HopDepth, first.HopDepth)
	}
	if events, runs := internalCounts(t, s); events != 1 || runs != 1 {
		t.Fatalf("store after re-emit = %d events %d runs, want 1/1 (deduped)", events, runs)
	}
}

func TestInternalSourceNoMatchingBindingIsNotFound(t *testing.T) {
	s := memstore.New()
	setupInternalBinding(t, s)
	src := &trigger.InternalSource{Store: s}

	_, err := src.Emit(t.Context(), "WS", trigger.InternalEvent{
		EventID: "lonely", EventType: "deploy.requested",
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Emit with no listener err = %v, want ErrNotFound", err)
	}
}

func TestInternalSourceHopDepthCapOverride(t *testing.T) {
	s := memstore.New()
	setupInternalBinding(t, s)
	var audit bytes.Buffer
	src := &trigger.InternalSource{Store: s, HopDepthCap: 1, Logger: slog.New(slog.NewTextHandler(&audit, nil))}

	first, err := src.Emit(t.Context(), "WS", trigger.InternalEvent{
		EventID: "cap-1", EventType: "issue.created",
	})
	if err != nil || first.Dropped {
		t.Fatalf("first Emit = %+v, %v; want dispatched at depth 1", first, err)
	}
	second, err := src.Emit(t.Context(), "WS", trigger.InternalEvent{
		EventID: "cap-2", EventType: "issue.created", ParentEventID: "cap-1",
	})
	if err != nil {
		t.Fatalf("second Emit: %v", err)
	}
	if !second.Dropped || second.HopDepth != 2 {
		t.Fatalf("second result = %+v, want dropped at depth 2 under cap 1", second)
	}
}
