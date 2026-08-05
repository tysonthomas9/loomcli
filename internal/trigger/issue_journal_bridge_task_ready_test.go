package trigger_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

// setupTaskReadyBinding seeds a prompt-agent binding on internal.task.ready so
// task.ready emissions have a listener to dispatch to.
func setupTaskReadyBinding(t *testing.T, s *memstore.Store) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "prompt-agent", Name: "prompt-agent",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "v1", DriverID: "prompt-agent", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b", ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := s.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "b-task-ready", Name: "b-task-ready",
		SourceKind: "internal", RouteKey: "internal." + trigger.TaskReadyEventType,
		SourceConfigRef: `{"roleName":"docs-assistant"}`,
		DriverID:        "prompt-agent", DriverVersionID: "v1", TargetEntrypoint: "run",
		ConcurrencyPolicy: domain.TriggerBindingConcurrencyAllow, Enabled: true,
	}); err != nil {
		t.Fatalf("Create trigger binding: %v", err)
	}
}

// TestIssueJournalBridgeEmitsTaskReady proves ITEM E: with EmitTaskReady on, a
// newly-created open task emits a task.ready internal event carrying the task id
// in its payload, dispatched to the internal.task.ready binding.
func TestIssueJournalBridgeEmitsTaskReady(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("200", "issue.create", "user:alice", "SANDBOX-7",
				`{"status":"open","title":"Write docs","repo":"acme/app"}`),
		}, next: "200"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")

	s := memstore.New()
	setupTaskReadyBinding(t, s)
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		IssueLookup: func(context.Context, string, string) (trigger.TaskReadySnapshot, error) {
			return trigger.TaskReadySnapshot{
				TaskID: "SANDBOX-7", Status: "open", IssueType: "task",
				SourceRepo: "acme/app", RepositoryRequired: false,
			}, nil
		},
	}

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// The create is not allowlisted for the normal re-emission here (no
	// internal.issue.created binding), so Emitted counts only that lane; the
	// task-ready lane is counted separately.
	if out.TaskReadyEmitted != 1 {
		t.Fatalf("result = %+v, want 1 task-ready emitted", out)
	}

	// A run was dispatched for the task-ready binding, and its payload carries
	// the task id (nested in the InternalSource envelope) plus the binding's
	// configured roleName is NOT here (roleName rides source_config_ref, resolved
	// by the run) — the run payload is the envelope with the emitter event.
	evs, err := s.TriggerEvents().List(t.Context(), "WS", store.TriggerEventFilter{})
	if err != nil {
		t.Fatalf("List events: %v", err)
	}
	var readyEvent *domain.TriggerEvent
	for _, e := range evs {
		if e.EventType == trigger.TaskReadyEventType {
			readyEvent = e
		}
	}
	if readyEvent == nil {
		t.Fatalf("no task.ready event persisted; events = %+v", evs)
	}
	if readyEvent.SubjectRef != "issue:SANDBOX-7" {
		t.Fatalf("task.ready subject = %q, want issue:SANDBOX-7", readyEvent.SubjectRef)
	}

	runs, err := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("List runs = %v, %v; want 1 run", runs, err)
	}
	var envelope struct {
		Event json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(runs[0].Payload, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var ev map[string]any
	if err := json.Unmarshal(envelope.Event, &ev); err != nil {
		t.Fatalf("decode emitter payload: %v", err)
	}
	if ev["taskId"] != "SANDBOX-7" {
		t.Fatalf("emitter payload taskId = %v, want SANDBOX-7 (claim target)", ev["taskId"])
	}
	if ev["sourceRepo"] != "acme/app" {
		t.Fatalf("emitter payload sourceRepo = %v, want acme/app", ev["sourceRepo"])
	}
	if ev["repositoryRequired"] != false {
		t.Fatalf("emitter payload repositoryRequired = %v, want false", ev["repositoryRequired"])
	}
}

// TestIssueJournalBridgeTaskReadySuppressesLiveEpics proves the journal lane
// consults the configured current projection before crossing Source.Emit. Both
// ready-entry journal actions for an epic advance normally but persist no
// task.ready event and dispatch no DriverRun. Ordinary tasks still dispatch for
// create/update/reopen/release/undefer, including the recovery release that can
// occur after startup reconciliation has completed.
func TestIssueJournalBridgeTaskReadySuppressesLiveEpics(t *testing.T) {
	tests := []struct {
		name      string
		action    string
		after     string
		issueType string
		wantRuns  int
	}{
		{name: "create epic", action: "issue.create", after: `{"status":"open","type":"epic"}`, issueType: "epic", wantRuns: 0},
		{name: "update epic", action: "issue.update", after: `{"status":"open"}`, issueType: " EPIC ", wantRuns: 0},
		{name: "release epic", action: "issue.release", after: `{"status":"open","assignee":""}`, issueType: "epic", wantRuns: 0},
		{name: "create task", action: "issue.create", after: `{"status":"open","type":"task"}`, issueType: "task", wantRuns: 1},
		{name: "update task", action: "issue.update", after: `{"status":"open"}`, issueType: "task", wantRuns: 1},
		{name: "reopen task", action: "issue.reopen", after: `{"status":"open"}`, issueType: "task", wantRuns: 1},
		{name: "release task", action: "issue.release", after: `{"status":"open","assignee":""}`, issueType: "task", wantRuns: 1},
		{name: "undefer task", action: "issue.undefer", after: `{"status":"open"}`, issueType: "task", wantRuns: 1},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eventID := fmt.Sprintf("epic-suppression-%d", i)
			taskID := fmt.Sprintf("TASK-%d", i)
			reader := &fakeIssueJournalReader{pages: map[string]journalPage{
				"": {events: []store.JournalEvent{
					issueEvent(eventID, tt.action, "user:alice", taskID, tt.after),
				}, next: eventID},
			}}
			cursors := newFixedCursorStore()
			seenStart(cursors, "WS")
			s := memstore.New()
			setupTaskReadyBinding(t, s)
			lookups := 0
			bridge := &trigger.IssueJournalBridge{
				Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
				WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
				IssueLookup: func(_ context.Context, ws, id string) (trigger.TaskReadySnapshot, error) {
					lookups++
					if ws != "WS" || id != taskID {
						t.Fatalf("IssueLookup workspace/task = %q/%q, want WS/%s", ws, id, taskID)
					}
					return trigger.TaskReadySnapshot{
						TaskID: id, Status: "open", IssueType: tt.issueType,
					}, nil
				},
			}

			out, err := bridge.RunOnce(t.Context())
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if lookups != 1 {
				t.Fatalf("IssueLookup calls = %d, want 1", lookups)
			}
			if out.TaskReadyEmitted != tt.wantRuns {
				t.Fatalf("TaskReadyEmitted = %d, want %d; result = %+v", out.TaskReadyEmitted, tt.wantRuns, out)
			}
			runs, err := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
			if err != nil || len(runs) != tt.wantRuns {
				t.Fatalf("DriverRuns = %v, %v; want %d", runs, err, tt.wantRuns)
			}
			events, err := s.TriggerEvents().List(t.Context(), "WS", store.TriggerEventFilter{})
			if err != nil {
				t.Fatalf("List trigger events: %v", err)
			}
			readyEvents := 0
			for _, event := range events {
				if event.EventType == trigger.TaskReadyEventType {
					readyEvents++
				}
			}
			if readyEvents != tt.wantRuns {
				t.Fatalf("persisted task.ready events = %d, want %d", readyEvents, tt.wantRuns)
			}
			if cursor, _ := cursors.Load("WS"); cursor != eventID {
				t.Fatalf("cursor = %q, want suppressed/handled event %q", cursor, eventID)
			}
		})
	}
}

func TestIssueJournalBridgeTaskReadySuppressesLaggedOpenJournalForNonOpenLiveTask(t *testing.T) {
	for _, status := range []string{"in_progress", "closed", "blocked"} {
		t.Run(status, func(t *testing.T) {
			eventID := "lagged-" + status
			reader := &fakeIssueJournalReader{pages: map[string]journalPage{
				"": {events: []store.JournalEvent{
					issueEvent(eventID, "issue.update", "user:alice", "TASK-LAGGED",
						`{"status":"open","repo":"acme/app"}`),
				}, next: eventID},
			}}
			cursors := newFixedCursorStore()
			seenStart(cursors, "WS")
			s := memstore.New()
			setupTaskReadyBinding(t, s)
			bridge := &trigger.IssueJournalBridge{
				Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
				WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
				IssueLookup: func(context.Context, string, string) (trigger.TaskReadySnapshot, error) {
					return trigger.TaskReadySnapshot{
						TaskID: "TASK-LAGGED", Status: status, IssueType: "task", SourceRepo: "acme/app",
					}, nil
				},
			}

			out, err := bridge.RunOnce(t.Context())
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if out.TaskReadyEmitted != 0 {
				t.Fatalf("result = %+v, want lagged open occurrence suppressed", out)
			}
			runs, err := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
			if err != nil || len(runs) != 0 {
				t.Fatalf("runs = %v, err=%v; want none for live status %q", runs, err, status)
			}
			if cursor, _ := cursors.Load("WS"); cursor != eventID {
				t.Fatalf("cursor = %q, want stale event handled through %q", cursor, eventID)
			}
		})
	}
}

// TestIssueJournalBridgeTaskReadyPayloadEnrichment proves the pinned claim-gate
// contract: the task.ready emitter payload carries taskId, status, hasDesign,
// labels and issueType pulled from the journal After snapshot (fleet-db wire
// keys design/labels/type). A task WITH a design and labels reports hasDesign
// true and its labels/type; the fields are always present with a stable type.
func TestIssueJournalBridgeTaskReadyPayloadEnrichment(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("500", "issue.update", "user:alice", "SANDBOX-11",
				`{"status":"open","title":"Fix it","type":"bug","design":"approved plan","labels":["urgent","backend"]}`),
		}, next: "500"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	s := memstore.New()
	setupTaskReadyBinding(t, s)
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
	}
	if _, err := bridge.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	runs, err := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("List runs = %v, %v; want 1 run", runs, err)
	}
	var envelope struct {
		Event json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(runs[0].Payload, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var ev struct {
		TaskID    string   `json:"taskId"`
		Status    string   `json:"status"`
		HasDesign bool     `json:"hasDesign"`
		Labels    []string `json:"labels"`
		IssueType string   `json:"issueType"`
	}
	if err := json.Unmarshal(envelope.Event, &ev); err != nil {
		t.Fatalf("decode emitter payload: %v", err)
	}
	if ev.TaskID != "SANDBOX-11" || ev.Status != "open" {
		t.Fatalf("taskId/status = %q/%q, want SANDBOX-11/open", ev.TaskID, ev.Status)
	}
	if !ev.HasDesign {
		t.Fatalf("hasDesign = false, want true (design body present)")
	}
	if ev.IssueType != "bug" {
		t.Fatalf("issueType = %q, want bug", ev.IssueType)
	}
	if len(ev.Labels) != 2 || ev.Labels[0] != "urgent" || ev.Labels[1] != "backend" {
		t.Fatalf("labels = %v, want [urgent backend]", ev.Labels)
	}
}

// TestIssueJournalBridgeTaskReadyPayloadZeroValues proves the contract fields are
// present with stable zero values for a bare task (no design/labels/type): a
// planner-lane task created empty reports hasDesign false and labels [] (never
// null), so the claim gate can compare without nil checks.
func TestIssueJournalBridgeTaskReadyPayloadZeroValues(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("501", "issue.create", "user:alice", "SANDBOX-12", `{"status":"open","title":"Plan me"}`),
		}, next: "501"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	s := memstore.New()
	setupTaskReadyBinding(t, s)
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
	}
	if _, err := bridge.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	runs, _ := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
	if len(runs) != 1 {
		t.Fatalf("want 1 run, got %d", len(runs))
	}
	var envelope struct {
		Event json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(runs[0].Payload, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	// labels must serialize as [] not null: assert on the raw JSON.
	if !json.Valid(envelope.Event) {
		t.Fatalf("emitter payload is not valid JSON: %s", envelope.Event)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Event, &probe); err != nil {
		t.Fatalf("decode emitter payload: %v", err)
	}
	if got := string(probe["labels"]); got != "[]" {
		t.Fatalf("labels raw = %s, want [] (stable empty array, never null)", got)
	}
	if got := string(probe["hasDesign"]); got != "false" {
		t.Fatalf("hasDesign raw = %s, want false", got)
	}
	if got := string(probe["issueType"]); got != `""` {
		t.Fatalf("issueType raw = %s, want empty string", got)
	}
	if got := string(probe["sourceRepo"]); got != `""` {
		t.Fatalf("sourceRepo raw = %s, want known empty string", got)
	}
}

// TestIssueJournalBridgeTaskReadyGatedOff proves default behavior is unchanged:
// with EmitTaskReady off, no task.ready event is emitted.
func TestIssueJournalBridgeTaskReadyGatedOff(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("201", "issue.create", "user:alice", "SANDBOX-8", `{"status":"open"}`),
		}, next: "201"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	s := memstore.New()
	setupTaskReadyBinding(t, s)
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: false,
	}
	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 0 {
		t.Fatalf("result = %+v, want 0 task-ready emitted when gated off", out)
	}
	evs, _ := s.TriggerEvents().List(t.Context(), "WS", store.TriggerEventFilter{})
	for _, e := range evs {
		if e.EventType == trigger.TaskReadyEventType {
			t.Fatalf("task.ready event emitted while gated off: %+v", e)
		}
	}
}

// TestIssueJournalBridgeTaskReadyOnlyForReadyEntryAction proves both halves of
// the readiness filter: closed/blocked snapshots do not fire, and assignment or
// claim actions do not become ready-entry events merely because a malformed or
// stale projection says open.
func TestIssueJournalBridgeTaskReadyOnlyForReadyEntryAction(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("300", "issue.update", "task-run:x", "SANDBOX-9", `{"status":"blocked"}`),
			issueEvent("301", "issue.close", "task-run:x", "SANDBOX-9", `{"status":"closed"}`),
			issueEvent("302", "issue.assign", "user:alice", "SANDBOX-9", `{"status":"open"}`),
			issueEvent("303", "issue.claim", "driver-run:x", "SANDBOX-9", `{"status":"open"}`),
		}, next: "303"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	s := memstore.New()
	setupTaskReadyBinding(t, s)
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
	}
	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 0 {
		t.Fatalf("result = %+v, want 0 task-ready (no open transition)", out)
	}
}

// TestIssueJournalBridgeTaskReadyOnUnblock proves an issue.update TO open (an
// unblock/reopen) emits task.ready.
func TestIssueJournalBridgeTaskReadyOnUnblock(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("400", "issue.update", "user:alice", "SANDBOX-10", `{"status":"open"}`),
		}, next: "400"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	s := memstore.New()
	setupTaskReadyBinding(t, s)
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
	}
	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 1 {
		t.Fatalf("result = %+v, want 1 task-ready on unblock", out)
	}
}

// TestIssueJournalBridgeTaskReadyDeltaUsesIssueLookup is the regression test
// for the 2026-07-07 approve-transition bug: an issue.update entry's After is a
// DELTA (here only {"status":"open"} — the plan-approval move), which says
// NOTHING about the card's design. The payload must be enriched from
// IssueLookup (the live card, which HAS a design) — emitting hasDesign=false
// would send the coder away and invite the planner to re-plan.
func TestIssueJournalBridgeTaskReadyDeltaUsesIssueLookup(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("600", "issue.update", "user:alice", "SANDBOX-13", `{"status":"open"}`),
		}, next: "600"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	s := memstore.New()
	setupTaskReadyBinding(t, s)
	var lookedUpWS, lookedUpID string
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		IssueLookup: func(_ context.Context, ws, id string) (trigger.TaskReadySnapshot, error) {
			lookedUpWS, lookedUpID = ws, id
			return trigger.TaskReadySnapshot{
				TaskID: id, Status: "open", HasDesign: true,
				Labels: []string{"backend"}, IssueType: "task", SourceRepo: "acme/app",
			}, nil
		},
	}
	if _, err := bridge.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if lookedUpWS != "WS" || lookedUpID != "SANDBOX-13" {
		t.Fatalf("lookup called with %q/%q, want WS/SANDBOX-13", lookedUpWS, lookedUpID)
	}
	runs, _ := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
	if len(runs) != 1 {
		t.Fatalf("want 1 run, got %d", len(runs))
	}
	var envelope struct {
		Event json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(runs[0].Payload, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var ev struct {
		HasDesign  bool     `json:"hasDesign"`
		Labels     []string `json:"labels"`
		IssueType  string   `json:"issueType"`
		SourceRepo string   `json:"sourceRepo"`
	}
	if err := json.Unmarshal(envelope.Event, &ev); err != nil {
		t.Fatalf("decode emitter payload: %v", err)
	}
	if !ev.HasDesign {
		t.Fatalf("hasDesign = false, want true (live card has a design)")
	}
	if len(ev.Labels) != 1 || ev.Labels[0] != "backend" {
		t.Fatalf("labels = %v, want [backend]", ev.Labels)
	}
	if ev.IssueType != "task" {
		t.Fatalf("issueType = %q, want task", ev.IssueType)
	}
	if ev.SourceRepo != "acme/app" {
		t.Fatalf("sourceRepo = %q, want acme/app", ev.SourceRepo)
	}
}

// TestIssueJournalBridgeTaskReadyDeltaWithoutLookupOmitsUnknowns proves the
// fail-honest half of the delta semantics: with no IssueLookup wired, the
// gating keys are OMITTED — absent means UNKNOWN, and the claim
// gate falls back to claim-then-check — never a lying false.
func TestIssueJournalBridgeTaskReadyDeltaWithoutLookupOmitsUnknowns(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("601", "issue.update", "user:alice", "SANDBOX-14", `{"status":"open"}`),
		}, next: "601"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	s := memstore.New()
	setupTaskReadyBinding(t, s)
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
	}
	if _, err := bridge.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	runs, _ := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
	if len(runs) != 1 {
		t.Fatalf("want 1 run, got %d", len(runs))
	}
	var envelope struct {
		Event json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(runs[0].Payload, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Event, &probe); err != nil {
		t.Fatalf("decode emitter payload: %v", err)
	}
	for _, key := range []string{"hasDesign", "labels", "issueType", "sourceRepo", "repositoryRequired"} {
		if _, present := probe[key]; present {
			t.Fatalf("%s present in delta payload without lookup — absent means unknown, a zero value here is a lie", key)
		}
	}
	if string(probe["taskId"]) != `"SANDBOX-14"` {
		t.Fatalf("taskId = %s, want SANDBOX-14", probe["taskId"])
	}
}

// TestIssueJournalBridgeTaskReadyLookupFailureRetriesBeforeClaim proves a live
// enrichment failure cannot silently erase repositoryRequired. In production
// the lookup also resolves repository cardinality; emitting a partial event for
// a repo-less multi-repo task would cross the prompt-agent's pre-claim guard.
func TestIssueJournalBridgeTaskReadyLookupFailureRetriesBeforeClaim(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("602", "issue.create", "user:alice", "SANDBOX-15", `{"status":"open","title":"Pick a repository"}`),
		}, next: "602"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	s := memstore.New()
	setupTaskReadyBinding(t, s)
	lookups := 0
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		IssueLookup: func(context.Context, string, string) (trigger.TaskReadySnapshot, error) {
			lookups++
			if lookups == 1 {
				return trigger.TaskReadySnapshot{}, errors.New("repository list unavailable")
			}
			return trigger.TaskReadySnapshot{
				TaskID: "SANDBOX-15", Status: "open", IssueType: "task",
				RepositoryRequired: true,
			}, nil
		},
	}

	if _, err := bridge.RunOnce(t.Context()); err == nil {
		t.Fatal("first RunOnce error = nil, want lookup failure")
	}
	runs, _ := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
	if len(runs) != 0 {
		t.Fatalf("runs after failed lookup = %d, want zero pre-claim dispatches", len(runs))
	}
	if cursor, _ := cursors.Load("WS"); cursor != "" {
		t.Fatalf("cursor after failed lookup = %q, want failed entry retained", cursor)
	}
	if out, err := bridge.RunOnce(t.Context()); err != nil || out.BackedOff != 1 || lookups != 1 {
		t.Fatalf("backoff result/error/lookups = %+v/%v/%d, want no lookup retry yet", out, err, lookups)
	}
	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("recovery RunOnce: %v", err)
	}
	if lookups != 2 || out.TaskReadyEmitted != 1 {
		t.Fatalf("recovery lookups/result = %d/%+v, want one successful retry", lookups, out)
	}
}

func TestIssueJournalBridgeTaskReadyDeletedIssueDoesNotPoisonCursor(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("602-gone", "issue.create", "user:alice", "TASK-GONE", `{"status":"open"}`),
			issueEvent("602-live", "issue.create", "user:alice", "TASK-LIVE", `{"status":"open"}`),
		}, next: "602-live"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	s := memstore.New()
	setupTaskReadyBinding(t, s)
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		IssueLookup: func(_ context.Context, _ string, id string) (trigger.TaskReadySnapshot, error) {
			if id == "TASK-GONE" {
				return trigger.TaskReadySnapshot{}, domain.ErrNotFound
			}
			return trigger.TaskReadySnapshot{TaskID: id, Status: "open", IssueType: "task"}, nil
		},
	}

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 1 {
		t.Fatalf("result = %+v, want only the live task emitted", out)
	}
	if cursor, _ := cursors.Load("WS"); cursor != "602-live" {
		t.Fatalf("cursor = %q, want deleted entry skipped through 602-live", cursor)
	}
	runs, err := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %v, err=%v; want one live task run", runs, err)
	}
}

func TestIssueJournalBridgeBlocksRepositoryRequiredTaskBeforeDispatch(t *testing.T) {
	for _, test := range []struct {
		name               string
		repositoryRequired bool
	}{
		{name: "snapshot already requires repository", repositoryRequired: true},
		// The lookup saw one repository and considered its implicit fallback
		// usable. Simulate deletion winning before the commit-time command: the
		// task must be blocked instead of dispatching into a zero-repo workspace.
		{name: "sole repository deleted after snapshot", repositoryRequired: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &fakeIssueJournalReader{pages: map[string]journalPage{
				"": {events: []store.JournalEvent{
					issueEvent("603", "issue.create", "user:alice", "PHASE4-TERRA-FRESH-20260718-10",
						`{"status":"open","title":"Testing HTML design"}`),
				}, next: "603"},
			}}
			cursors := newFixedCursorStore()
			seenStart(cursors, "WS")
			s := memstore.New()
			setupTaskReadyBinding(t, s)
			blockCalls := 0
			bridge := &trigger.IssueJournalBridge{
				Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
				WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
				IssueLookup: func(context.Context, string, string) (trigger.TaskReadySnapshot, error) {
					return trigger.TaskReadySnapshot{
						TaskID: "PHASE4-TERRA-FRESH-20260718-10", Status: "open", IssueType: "task",
						RepositoryRequired: test.repositoryRequired,
					}, nil
				},
				RepositoryRequiredBlocker: func(_ context.Context, ws, taskID string) (trigger.TaskReadyRepositoryRequiredResult, error) {
					blockCalls++
					if ws != "WS" || taskID != "PHASE4-TERRA-FRESH-20260718-10" {
						t.Fatalf("block command = %q/%q", ws, taskID)
					}
					return trigger.TaskReadyRepositoryRequiredResult{Blocked: true}, nil
				},
			}

			out, err := bridge.RunOnce(t.Context())
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if blockCalls != 1 || out.TaskReadyBlocked != 1 || out.TaskReadyEmitted != 0 {
				t.Fatalf("block calls/result = %d/%+v, want one block and no task.ready", blockCalls, out)
			}
			if cursor, _ := cursors.Load("WS"); cursor != "603" {
				t.Fatalf("cursor = %q, want blocked entry durably handled", cursor)
			}
			runs, err := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
			if err != nil || len(runs) != 0 {
				t.Fatalf("runs after repository block = %v, %v; want none", runs, err)
			}
		})
	}
}

func TestIssueJournalBridgeRepositoryAdmissionRaceDispatchesCanonicalAssignedRepo(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("604", "issue.create", "user:alice", "TASK-604",
				`{"status":"open","title":"Stale repo-less snapshot","labels":["stale"]}`),
		}, next: "604"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	s := memstore.New()
	setupTaskReadyBinding(t, s)
	canonicalAt := time.Date(2026, 7, 18, 21, 4, 0, 0, time.UTC)
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		IssueLookup: func(context.Context, string, string) (trigger.TaskReadySnapshot, error) {
			// This is the snapshot that caused admission. Repository assignment
			// commits after the lookup and before the atomic block command.
			return trigger.TaskReadySnapshot{
				TaskID: "TASK-604", Status: "open", IssueType: "task",
				Labels: []string{"stale"}, RepositoryRequired: true,
			}, nil
		},
		RepositoryRequiredBlocker: func(context.Context, string, string) (trigger.TaskReadyRepositoryRequiredResult, error) {
			return trigger.TaskReadyRepositoryRequiredResult{DispatchReady: &trigger.TaskReadySnapshot{
				TaskID: "TASK-604", Status: "open", IssueType: "bug", HasDesign: true,
				Labels: []string{"canonical", "phase4"}, SourceRepo: "fleet-source",
				// A defensive true here proves the consumer derives this fact from
				// DispatchReady instead of copying a stale repository-required flag.
				RepositoryRequired: true, UpdatedAt: canonicalAt,
			}}, nil
		},
	}

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 1 || out.TaskReadyBlocked != 0 {
		t.Fatalf("result = %+v, want canonical dispatch and no block", out)
	}
	runs, err := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %v, err=%v; want one", runs, err)
	}
	var envelope struct {
		Event json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(runs[0].Payload, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Event, &payload); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if got := string(payload["status"]); got != `"open"` {
		t.Fatalf("canonical status = %s, want open", got)
	}
	if got := string(payload["sourceRepo"]); got != `"fleet-source"` {
		t.Fatalf("canonical sourceRepo = %s", got)
	}
	if got := string(payload["repositoryRequired"]); got != "false" {
		t.Fatalf("canonical repositoryRequired = %s, want false", got)
	}
	if got := string(payload["hasDesign"]); got != "true" {
		t.Fatalf("canonical hasDesign = %s, want true", got)
	}
	if got := string(payload["labels"]); got != `["canonical","phase4"]` {
		t.Fatalf("canonical labels = %s", got)
	}
}

func TestIssueJournalBridgeStartupReconcileBlocksExistingRepositoryRequiredTask(t *testing.T) {
	for _, repositoryRequired := range []bool{true, false} {
		t.Run(fmt.Sprintf("pre-read-required-%t", repositoryRequired), func(t *testing.T) {
			reader := &fakeIssueJournalReader{pages: map[string]journalPage{"900": {next: "900"}}}
			cursors := newFixedCursorStore()
			cursors.Save("WS", "900")
			s := memstore.New()
			setupTaskReadyBinding(t, s)
			blockCalls := 0
			bridge := &trigger.IssueJournalBridge{
				Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
				WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
				ReadySnapshots: func(context.Context, string) ([]trigger.TaskReadySnapshot, error) {
					return []trigger.TaskReadySnapshot{{
						TaskID: "PHASE4-TERRA-FRESH-20260718-10", Status: "open", IssueType: "task",
						RepositoryRequired: repositoryRequired, UpdatedAt: time.Date(2026, 7, 18, 20, 0, 0, 0, time.UTC),
					}}, nil
				},
				RepositoryRequiredBlocker: func(context.Context, string, string) (trigger.TaskReadyRepositoryRequiredResult, error) {
					blockCalls++
					return trigger.TaskReadyRepositoryRequiredResult{Blocked: true}, nil
				},
			}

			out, err := bridge.RunOnce(t.Context())
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if blockCalls != 1 || out.TaskReadyBlocked != 1 || out.TaskReadyEmitted != 0 {
				t.Fatalf("calls/result = %d/%+v, want existing task blocked without dispatch", blockCalls, out)
			}
			runs, err := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
			if err != nil || len(runs) != 0 {
				t.Fatalf("runs after startup block = %v, %v; want none", runs, err)
			}
		})
	}
}

func TestIssueJournalBridgeStartupRepositoryCountRaceDispatchesWithoutIssueEvent(t *testing.T) {
	// The journal is already at its tail. A workspace repository count changing
	// from zero to one does not write an issue event, so the admission command's
	// DispatchReady result is the only opportunity to launch this task.
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{"920": {next: "920"}}}
	cursors := newFixedCursorStore()
	cursors.Save("WS", "920")
	s := memstore.New()
	setupTaskReadyBinding(t, s)
	updatedAt := time.Date(2026, 7, 18, 21, 20, 0, 0, time.UTC)
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		ReadySnapshots: func(context.Context, string) ([]trigger.TaskReadySnapshot, error) {
			return []trigger.TaskReadySnapshot{{
				TaskID: "TASK-920", Status: "open", IssueType: "task",
				RepositoryRequired: true, UpdatedAt: updatedAt,
			}}, nil
		},
		RepositoryRequiredBlocker: func(context.Context, string, string) (trigger.TaskReadyRepositoryRequiredResult, error) {
			return trigger.TaskReadyRepositoryRequiredResult{DispatchReady: &trigger.TaskReadySnapshot{
				TaskID: "TASK-920", Status: "open", IssueType: "task",
				SourceRepo: "", RepositoryRequired: true, UpdatedAt: updatedAt,
			}}, nil
		},
	}

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 1 || out.TaskReadyBlocked != 0 {
		t.Fatalf("result = %+v, want one single-repo fallback dispatch", out)
	}
	runs, err := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %v, err=%v; want one despite no issue journal event", runs, err)
	}
	var envelope struct {
		Event json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(runs[0].Payload, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Event, &payload); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if got := string(payload["sourceRepo"]); got != `""` {
		t.Fatalf("single-repo sourceRepo = %s, want known empty", got)
	}
	if got := string(payload["repositoryRequired"]); got != "false" {
		t.Fatalf("single-repo repositoryRequired = %s, want false", got)
	}
}

// TestIssueJournalBridgeReconcilesCurrentReadyPastCursor is the regression for
// enabling the task-ready lane after the shared journal cursor already passed
// an open task. Reconciliation emits the CURRENT Ready generation once; a new
// bridge instance repeats the stable synthetic ID and dispatch dedup preserves
// one run.
func TestIssueJournalBridgeReconcilesCurrentReadyPastCursor(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"900": {next: "900"},
	}}
	cursors := newFixedCursorStore()
	cursors.Save("WS", "900")
	s := memstore.New()
	setupTaskReadyBinding(t, s)
	updatedAt := time.Date(2026, 7, 18, 20, 0, 0, 123, time.UTC)
	list := func(_ context.Context, ws string) ([]trigger.TaskReadySnapshot, error) {
		if ws != "WS" {
			t.Fatalf("ReadySnapshots workspace = %q, want WS", ws)
		}
		return []trigger.TaskReadySnapshot{
			{
				TaskID: "PHASE4-TERRA-FRESH-20260718-10", Status: "open",
				HasDesign: false, Labels: []string{"terra", "phase4", "terra"},
				IssueType: "task", SourceRepo: "", RepositoryRequired: true, UpdatedAt: updatedAt,
			},
			{
				TaskID: "PHASE4-TERRA-FRESH-20260718-EPIC", Status: "open",
				IssueType: "epic", UpdatedAt: updatedAt,
			},
		}, nil
	}
	newBridge := func() *trigger.IssueJournalBridge {
		return &trigger.IssueJournalBridge{
			Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
			WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
			ReadySnapshots: list,
		}
	}

	out, err := newBridge().RunOnce(t.Context())
	if err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 1 {
		t.Fatalf("first result = %+v, want one reconciled task.ready", out)
	}
	// Simulate a serve restart. The scan runs again, but its content-derived ID
	// is identical and Automation/dispatch idempotency must retain one run.
	out, err = newBridge().RunOnce(t.Context())
	if err != nil {
		t.Fatalf("restart RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 1 {
		t.Fatalf("restart result = %+v, want one deduped emission attempt", out)
	}
	runs, err := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs after restart = %v, %v; want exactly one", runs, err)
	}
	var envelope struct {
		Event json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(runs[0].Payload, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Event, &payload); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if got := string(payload["sourceRepo"]); got != `""` {
		t.Fatalf("sourceRepo = %s, want explicit known-empty string", got)
	}
	if got := string(payload["repositoryRequired"]); got != "true" {
		t.Fatalf("repositoryRequired = %s, want true for a repo-less multi-repo task", got)
	}
	if got := string(payload["labels"]); got != `["phase4","terra"]` {
		t.Fatalf("canonical labels = %s, want sorted/deduped labels", got)
	}
}

func TestIssueJournalBridgeReconcileSuppressesSameJournalGeneration(t *testing.T) {
	updatedAt := time.Date(2026, 7, 18, 20, 1, 0, 0, time.UTC)
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("910", "issue.create", "user:alice", "TASK-910",
				`{"status":"open","updated_at":"2026-07-18T20:01:00Z","repo":"acme/app"}`),
		}, next: "910"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	s := memstore.New()
	setupTaskReadyBinding(t, s)
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		ReadySnapshots: func(context.Context, string) ([]trigger.TaskReadySnapshot, error) {
			return []trigger.TaskReadySnapshot{{
				TaskID: "TASK-910", Status: "open", IssueType: "task",
				SourceRepo: "acme/app", UpdatedAt: updatedAt,
			}}, nil
		},
	}
	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 1 {
		t.Fatalf("result = %+v, want only reconciled emission", out)
	}
	runs, _ := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want one synthetic/natural generation", len(runs))
	}
}

// Typed Work Item recovery snapshots intentionally omit updated_at. The live
// lookup is the canonical fallback that proves the startup Ready snapshot and
// the catch-up release entry describe the same generation; admitting both
// would create two prompt-agent fanouts for one recovered task.
func TestIssueJournalBridgeReconcileSuppressesReleaseWithoutUpdatedAt(t *testing.T) {
	updatedAt := time.Date(2026, 7, 18, 20, 2, 0, 0, time.UTC)
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("911", "issue.release", "driver-run:stale", "TASK-911",
				`{"status":"open","assignee":""}`),
		}, next: "911"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	s := memstore.New()
	setupTaskReadyBinding(t, s)
	lookups := 0
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		ReadySnapshots: func(context.Context, string) ([]trigger.TaskReadySnapshot, error) {
			return []trigger.TaskReadySnapshot{{
				TaskID: "TASK-911", Status: "open", IssueType: "task", UpdatedAt: updatedAt,
			}}, nil
		},
		IssueLookup: func(context.Context, string, string) (trigger.TaskReadySnapshot, error) {
			lookups++
			return trigger.TaskReadySnapshot{
				TaskID: "TASK-911", Status: "open", IssueType: "task", UpdatedAt: updatedAt,
			}, nil
		},
	}
	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 1 || lookups != 1 {
		t.Fatalf("result/lookups = %+v/%d, want one synthetic emission and one generation lookup", out, lookups)
	}
	runs, err := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs after snapshot plus release catch-up = %v, %v; want exactly one", runs, err)
	}
}

func TestIssueJournalBridgeReconcileEmitsReleaseFromNewerLiveGeneration(t *testing.T) {
	reconciledAt := time.Date(2026, 7, 18, 20, 3, 0, 0, time.UTC)
	releasedAt := reconciledAt.Add(time.Minute)
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("912", "issue.release", "driver-run:newer", "TASK-912",
				`{"status":"open","assignee":""}`),
		}, next: "912"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	s := memstore.New()
	setupTaskReadyBinding(t, s)
	lookups := 0
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		ReadySnapshots: func(context.Context, string) ([]trigger.TaskReadySnapshot, error) {
			return []trigger.TaskReadySnapshot{{
				TaskID: "TASK-912", Status: "open", IssueType: "task", UpdatedAt: reconciledAt,
			}}, nil
		},
		IssueLookup: func(context.Context, string, string) (trigger.TaskReadySnapshot, error) {
			lookups++
			return trigger.TaskReadySnapshot{
				TaskID: "TASK-912", Status: "open", IssueType: "task", UpdatedAt: releasedAt,
			}, nil
		},
	}
	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 2 || lookups != 2 {
		t.Fatalf("result/lookups = %+v/%d, want synthetic plus newer release generation", out, lookups)
	}
	runs, err := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs for distinct Ready generations = %v, %v; want two", runs, err)
	}
}

func TestIssueJournalBridgeReconcileFailureRetries(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{"50": {next: "50"}}}
	cursors := newFixedCursorStore()
	cursors.Save("WS", "50")
	s := memstore.New()
	setupTaskReadyBinding(t, s)
	calls := 0
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		ReadySnapshots: func(context.Context, string) ([]trigger.TaskReadySnapshot, error) {
			calls++
			if calls == 1 {
				return nil, errors.New("ready unavailable")
			}
			return []trigger.TaskReadySnapshot{{TaskID: "TASK-50", Status: "open"}}, nil
		},
	}
	if _, err := bridge.RunOnce(t.Context()); err == nil {
		t.Fatal("first RunOnce error = nil, want ready-list failure")
	}
	backedOff, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("backoff RunOnce: %v", err)
	}
	if calls != 1 || backedOff.BackedOff != 1 {
		t.Fatalf("calls/result = %d/%+v, want one backoff pass", calls, backedOff)
	}
	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("retry RunOnce: %v", err)
	}
	if calls != 2 || out.TaskReadyEmitted != 1 {
		t.Fatalf("calls/result = %d/%+v, want delayed retry and one emission", calls, out)
	}
}
