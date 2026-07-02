package trigger_test

import (
	"encoding/json"
	"testing"

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
		SourceConfigRef:   `{"roleName":"docs-assistant"}`,
		DriverID:          "prompt-agent", DriverVersionID: "v1", TargetEntrypoint: "run",
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

// TestIssueJournalBridgeTaskReadyOnlyForOpenStatus proves the readiness filter:
// a close (status closed) or block (status blocked) never emits task.ready.
func TestIssueJournalBridgeTaskReadyOnlyForOpenStatus(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("300", "issue.update", "task-run:x", "SANDBOX-9", `{"status":"blocked"}`),
			issueEvent("301", "issue.close", "task-run:x", "SANDBOX-9", `{"status":"closed"}`),
		}, next: "301"},
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
