package trigger_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func runTaskReadyBridge(t *testing.T, bridge *trigger.IssueJournalBridge) (*trigger.IssueJournalSweepResult, error) {
	t.Helper()
	if bridge.EmitTaskReady {
		if bridge.ReadySnapshots == nil {
			bridge.ReadySnapshots = func(context.Context, string) ([]trigger.TaskReadySnapshot, error) { return nil, nil }
		}
		if bridge.IssueLookup == nil {
			bridge.IssueLookup = func(ctx context.Context, workspace, taskID string) (trigger.TaskReadySnapshot, error) {
				snapshots, err := bridge.ReadySnapshots(ctx, workspace)
				if err != nil {
					return trigger.TaskReadySnapshot{}, err
				}
				for _, snapshot := range snapshots {
					if snapshot.TaskID == taskID {
						return snapshot, nil
					}
				}
				return trigger.TaskReadySnapshot{}, domain.ErrNotFound
			}
		}
		if bridge.RepositoryRequiredBlocker == nil {
			bridge.RepositoryRequiredBlocker = func(ctx context.Context, workspace, taskID string) (trigger.TaskReadyRepositoryRequiredResult, error) {
				snapshot, err := bridge.IssueLookup(ctx, workspace, taskID)
				if err != nil {
					return trigger.TaskReadyRepositoryRequiredResult{}, err
				}
				return trigger.TaskReadyRepositoryRequiredResult{DispatchReady: &snapshot}, nil
			}
		}
	}
	return bridge.RunOnce(t.Context())
}

// TestIssueJournalBridgeEmitsTaskReady proves ITEM E: with EmitTaskReady on, a
// newly-created open task emits a task.ready internal event carrying the task id.
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
	emitter := &capturingInternalEmitter{}
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: emitter, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		IssueLookup: func(context.Context, string, string) (trigger.TaskReadySnapshot, error) {
			return trigger.TaskReadySnapshot{
				TaskID: "SANDBOX-7", Status: "open", IssueType: "task",
				SourceRepo: "acme/app", RepositoryRequired: false,
			}, nil
		},
	}

	out, err := runTaskReadyBridge(t, bridge)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// The create is not allowlisted for the normal re-emission here (no
	// internal.issue.created binding), so Emitted counts only that lane; the
	// task-ready lane is counted separately.
	if out.TaskReadyEmitted != 1 {
		t.Fatalf("result = %+v, want 1 task-ready emitted", out)
	}

	readyEvents := emitter.eventsOfType(trigger.TaskReadyEventType)
	if len(readyEvents) != 1 || readyEvents[0].SubjectRef != "issue:SANDBOX-7" {
		t.Fatalf("task.ready events = %+v, want one for issue:SANDBOX-7", readyEvents)
	}
	var ev map[string]any
	if err := json.Unmarshal(readyEvents[0].Payload, &ev); err != nil {
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
			emitter := &capturingInternalEmitter{}
			lookups := 0
			bridge := &trigger.IssueJournalBridge{
				Store: s, Source: emitter, Reader: reader,
				WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
				IssueLookup: func(_ context.Context, ws, id string) (trigger.TaskReadySnapshot, error) {
					lookups++
					if ws != "WS" || id != taskID {
						t.Fatalf("IssueLookup workspace/task = %q/%q, want WS/%s", ws, id, taskID)
					}
					return trigger.TaskReadySnapshot{
						TaskID: id, Status: "open", IssueType: tt.issueType, SourceRepo: "acme/app",
					}, nil
				},
			}

			out, err := runTaskReadyBridge(t, bridge)
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if lookups != 1 {
				t.Fatalf("IssueLookup calls = %d, want 1", lookups)
			}
			if out.TaskReadyEmitted != tt.wantRuns {
				t.Fatalf("TaskReadyEmitted = %d, want %d; result = %+v", out.TaskReadyEmitted, tt.wantRuns, out)
			}
			if readyEvents := emitter.eventsOfType(trigger.TaskReadyEventType); len(readyEvents) != tt.wantRuns {
				t.Fatalf("task.ready emissions = %d, want %d", len(readyEvents), tt.wantRuns)
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
			emitter := &capturingInternalEmitter{}
			bridge := &trigger.IssueJournalBridge{
				Store: s, Source: emitter, Reader: reader,
				WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
				IssueLookup: func(context.Context, string, string) (trigger.TaskReadySnapshot, error) {
					return trigger.TaskReadySnapshot{
						TaskID: "TASK-LAGGED", Status: status, IssueType: "task", SourceRepo: "acme/app",
					}, nil
				},
			}

			out, err := runTaskReadyBridge(t, bridge)
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if out.TaskReadyEmitted != 0 {
				t.Fatalf("result = %+v, want lagged open occurrence suppressed", out)
			}
			if events := emitter.eventsOfType(trigger.TaskReadyEventType); len(events) != 0 {
				t.Fatalf("task.ready emissions = %+v; want none for live status %q", events, status)
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
	emitter := &capturingInternalEmitter{}
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: emitter, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		IssueLookup: func(context.Context, string, string) (trigger.TaskReadySnapshot, error) {
			return trigger.TaskReadySnapshot{
				TaskID: "SANDBOX-11", Status: "open", HasDesign: true,
				Labels: []string{"urgent", "backend"}, IssueType: "bug", SourceRepo: "acme/app",
			}, nil
		},
	}
	if _, err := runTaskReadyBridge(t, bridge); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	readyEvents := emitter.eventsOfType(trigger.TaskReadyEventType)
	if len(readyEvents) != 1 {
		t.Fatalf("task.ready emissions = %d, want 1", len(readyEvents))
	}
	var ev struct {
		TaskID    string   `json:"taskId"`
		Status    string   `json:"status"`
		HasDesign bool     `json:"hasDesign"`
		Labels    []string `json:"labels"`
		IssueType string   `json:"issueType"`
	}
	if err := json.Unmarshal(readyEvents[0].Payload, &ev); err != nil {
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
	if len(ev.Labels) != 2 || ev.Labels[0] != "backend" || ev.Labels[1] != "urgent" {
		t.Fatalf("labels = %v, want canonical [backend urgent]", ev.Labels)
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
	emitter := &capturingInternalEmitter{}
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: emitter, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		IssueLookup: func(context.Context, string, string) (trigger.TaskReadySnapshot, error) {
			return trigger.TaskReadySnapshot{TaskID: "SANDBOX-12", Status: "open"}, nil
		},
		RepositoryRequiredBlocker: func(context.Context, string, string) (trigger.TaskReadyRepositoryRequiredResult, error) {
			return trigger.TaskReadyRepositoryRequiredResult{DispatchReady: &trigger.TaskReadySnapshot{
				TaskID: "SANDBOX-12", Status: "open",
			}}, nil
		},
	}
	if _, err := runTaskReadyBridge(t, bridge); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	readyEvents := emitter.eventsOfType(trigger.TaskReadyEventType)
	if len(readyEvents) != 1 {
		t.Fatalf("task.ready emissions = %d, want 1", len(readyEvents))
	}
	// labels must serialize as [] not null: assert on the raw JSON.
	if !json.Valid(readyEvents[0].Payload) {
		t.Fatalf("emitter payload is not valid JSON: %s", readyEvents[0].Payload)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(readyEvents[0].Payload, &probe); err != nil {
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
	emitter := &capturingInternalEmitter{}
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: emitter, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: false,
	}
	out, err := runTaskReadyBridge(t, bridge)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 0 {
		t.Fatalf("result = %+v, want 0 task-ready emitted when gated off", out)
	}
	if events := emitter.eventsOfType(trigger.TaskReadyEventType); len(events) != 0 {
		t.Fatalf("task.ready emissions while gated off = %+v, want none", events)
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
	emitter := &capturingInternalEmitter{}
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: emitter, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		IssueLookup: func(context.Context, string, string) (trigger.TaskReadySnapshot, error) {
			return trigger.TaskReadySnapshot{TaskID: "SANDBOX-9", Status: "open", IssueType: "task", SourceRepo: "acme/app"}, nil
		},
	}
	out, err := runTaskReadyBridge(t, bridge)
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
	emitter := &capturingInternalEmitter{}
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: emitter, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		IssueLookup: func(context.Context, string, string) (trigger.TaskReadySnapshot, error) {
			return trigger.TaskReadySnapshot{TaskID: "SANDBOX-10", Status: "open", IssueType: "task", SourceRepo: "acme/app"}, nil
		},
	}
	out, err := runTaskReadyBridge(t, bridge)
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
	emitter := &capturingInternalEmitter{}
	var lookedUpWS, lookedUpID string
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: emitter, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		IssueLookup: func(_ context.Context, ws, id string) (trigger.TaskReadySnapshot, error) {
			lookedUpWS, lookedUpID = ws, id
			return trigger.TaskReadySnapshot{
				TaskID: id, Status: "open", HasDesign: true,
				Labels: []string{"backend"}, IssueType: "task", SourceRepo: "acme/app",
			}, nil
		},
	}
	if _, err := runTaskReadyBridge(t, bridge); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if lookedUpWS != "WS" || lookedUpID != "SANDBOX-13" {
		t.Fatalf("lookup called with %q/%q, want WS/SANDBOX-13", lookedUpWS, lookedUpID)
	}
	readyEvents := emitter.eventsOfType(trigger.TaskReadyEventType)
	if len(readyEvents) != 1 {
		t.Fatalf("task.ready emissions = %d, want 1", len(readyEvents))
	}
	var ev struct {
		HasDesign  bool     `json:"hasDesign"`
		Labels     []string `json:"labels"`
		IssueType  string   `json:"issueType"`
		SourceRepo string   `json:"sourceRepo"`
	}
	if err := json.Unmarshal(readyEvents[0].Payload, &ev); err != nil {
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

func TestIssueJournalBridgeTaskReadyWithoutCurrentProjectionFailsClosed(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {events: []store.JournalEvent{
			issueEvent("601", "issue.update", "user:alice", "SANDBOX-14", `{"status":"open"}`),
		}, next: "601"},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	s := memstore.New()
	emitter := &capturingInternalEmitter{}
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: emitter, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
	}
	bridge.ReadySnapshots = func(context.Context, string) ([]trigger.TaskReadySnapshot, error) { return nil, nil }
	bridge.RepositoryRequiredBlocker = func(context.Context, string, string) (trigger.TaskReadyRepositoryRequiredResult, error) {
		return trigger.TaskReadyRepositoryRequiredResult{}, nil
	}
	if _, err := bridge.RunOnce(t.Context()); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("RunOnce error = %v, want fail-closed ErrInvalid", err)
	}
	if events := emitter.eventsOfType(trigger.TaskReadyEventType); len(events) != 0 {
		t.Fatalf("task.ready emissions without current projection = %+v, want none", events)
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
	emitter := &capturingInternalEmitter{}
	lookups := 0
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: emitter, Reader: reader,
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
		RepositoryRequiredBlocker: func(context.Context, string, string) (trigger.TaskReadyRepositoryRequiredResult, error) {
			return trigger.TaskReadyRepositoryRequiredResult{DispatchReady: &trigger.TaskReadySnapshot{
				TaskID: "SANDBOX-15", Status: "open", IssueType: "task",
			}}, nil
		},
	}

	if _, err := runTaskReadyBridge(t, bridge); err == nil {
		t.Fatal("first RunOnce error = nil, want lookup failure")
	}
	if events := emitter.eventsOfType(trigger.TaskReadyEventType); len(events) != 0 {
		t.Fatalf("task.ready emissions after failed lookup = %+v, want none", events)
	}
	if cursor, _ := cursors.Load("WS"); cursor != "" {
		t.Fatalf("cursor after failed lookup = %q, want failed entry retained", cursor)
	}
	if out, err := runTaskReadyBridge(t, bridge); err != nil || out.BackedOff != 1 || lookups != 1 {
		t.Fatalf("backoff result/error/lookups = %+v/%v/%d, want no lookup retry yet", out, err, lookups)
	}
	out, err := runTaskReadyBridge(t, bridge)
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
	emitter := &capturingInternalEmitter{}
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: emitter, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		IssueLookup: func(_ context.Context, _ string, id string) (trigger.TaskReadySnapshot, error) {
			if id == "TASK-GONE" {
				return trigger.TaskReadySnapshot{}, domain.ErrNotFound
			}
			return trigger.TaskReadySnapshot{TaskID: id, Status: "open", IssueType: "task"}, nil
		},
	}

	out, err := runTaskReadyBridge(t, bridge)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 1 {
		t.Fatalf("result = %+v, want only the live task emitted", out)
	}
	if cursor, _ := cursors.Load("WS"); cursor != "602-live" {
		t.Fatalf("cursor = %q, want deleted entry skipped through 602-live", cursor)
	}
	if readyEvents := emitter.eventsOfType(trigger.TaskReadyEventType); len(readyEvents) != 1 {
		t.Fatalf("task.ready emissions = %d, want one live task", len(readyEvents))
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
			emitter := &capturingInternalEmitter{}
			blockCalls := 0
			bridge := &trigger.IssueJournalBridge{
				Store: s, Source: emitter, Reader: reader,
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

			out, err := runTaskReadyBridge(t, bridge)
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if blockCalls != 1 || out.TaskReadyBlocked != 1 || out.TaskReadyEmitted != 0 {
				t.Fatalf("block calls/result = %d/%+v, want one block and no task.ready", blockCalls, out)
			}
			if cursor, _ := cursors.Load("WS"); cursor != "603" {
				t.Fatalf("cursor = %q, want blocked entry durably handled", cursor)
			}
			if events := emitter.eventsOfType(trigger.TaskReadyEventType); len(events) != 0 {
				t.Fatalf("task.ready emissions after repository block = %+v, want none", events)
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
	emitter := &capturingInternalEmitter{}
	canonicalAt := time.Date(2026, 7, 18, 21, 4, 0, 0, time.UTC)
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: emitter, Reader: reader,
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

	out, err := runTaskReadyBridge(t, bridge)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 1 || out.TaskReadyBlocked != 0 {
		t.Fatalf("result = %+v, want canonical dispatch and no block", out)
	}
	readyEvents := emitter.eventsOfType(trigger.TaskReadyEventType)
	if len(readyEvents) != 1 {
		t.Fatalf("task.ready emissions = %+v, want one", readyEvents)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(readyEvents[0].Payload, &payload); err != nil {
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
			emitter := &capturingInternalEmitter{}
			blockCalls := 0
			bridge := &trigger.IssueJournalBridge{
				Store: s, Source: emitter, Reader: reader,
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

			out, err := runTaskReadyBridge(t, bridge)
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if blockCalls != 1 || out.TaskReadyBlocked != 1 || out.TaskReadyEmitted != 0 {
				t.Fatalf("calls/result = %d/%+v, want existing task blocked without dispatch", blockCalls, out)
			}
			if events := emitter.eventsOfType(trigger.TaskReadyEventType); len(events) != 0 {
				t.Fatalf("task.ready emissions after startup block = %+v, want none", events)
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
	emitter := &capturingInternalEmitter{}
	updatedAt := time.Date(2026, 7, 18, 21, 20, 0, 0, time.UTC)
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: emitter, Reader: reader,
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

	out, err := runTaskReadyBridge(t, bridge)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 1 || out.TaskReadyBlocked != 0 {
		t.Fatalf("result = %+v, want one single-repo fallback dispatch", out)
	}
	readyEvents := emitter.eventsOfType(trigger.TaskReadyEventType)
	if len(readyEvents) != 1 {
		t.Fatalf("task.ready emissions = %+v, want one despite no issue journal event", readyEvents)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(readyEvents[0].Payload, &payload); err != nil {
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
// bridge instance repeats the stable synthetic ID. Automation owns admission
// deduplication; this bridge test proves the two emission attempts are stable.
func TestIssueJournalBridgeReconcilesCurrentReadyPastCursor(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"900": {next: "900"},
	}}
	cursors := newFixedCursorStore()
	cursors.Save("WS", "900")
	s := memstore.New()
	emitter := &capturingInternalEmitter{}
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
			Store: s, Source: emitter, Reader: reader,
			WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
			ReadySnapshots: list,
		}
	}

	out, err := runTaskReadyBridge(t, newBridge())
	if err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 1 {
		t.Fatalf("first result = %+v, want one reconciled task.ready", out)
	}
	// Simulate a serve restart. The scan runs again with the same content-derived
	// ID so Automation can deduplicate it at its own interface.
	out, err = runTaskReadyBridge(t, newBridge())
	if err != nil {
		t.Fatalf("restart RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 1 {
		t.Fatalf("restart result = %+v, want one deduped emission attempt", out)
	}
	readyEvents := emitter.eventsOfType(trigger.TaskReadyEventType)
	if len(readyEvents) != 2 {
		t.Fatalf("task.ready emissions after restart = %+v, want two attempts", readyEvents)
	}
	if readyEvents[0].EventID == "" || readyEvents[0].EventID != readyEvents[1].EventID {
		t.Fatalf("event IDs = %q/%q, want one stable non-empty ID", readyEvents[0].EventID, readyEvents[1].EventID)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(readyEvents[0].Payload, &payload); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if got := string(payload["sourceRepo"]); got != `""` {
		t.Fatalf("sourceRepo = %s, want explicit known-empty string", got)
	}
	if got := string(payload["repositoryRequired"]); got != "false" {
		t.Fatalf("repositoryRequired = %s, want false after commit-time admission", got)
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
	emitter := &capturingInternalEmitter{}
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: emitter, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		ReadySnapshots: func(context.Context, string) ([]trigger.TaskReadySnapshot, error) {
			return []trigger.TaskReadySnapshot{{
				TaskID: "TASK-910", Status: "open", IssueType: "task",
				SourceRepo: "acme/app", UpdatedAt: updatedAt,
			}}, nil
		},
	}
	out, err := runTaskReadyBridge(t, bridge)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 1 {
		t.Fatalf("result = %+v, want only reconciled emission", out)
	}
	if events := emitter.eventsOfType(trigger.TaskReadyEventType); len(events) != 1 {
		t.Fatalf("task.ready emissions = %+v, want one synthetic/natural generation", events)
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
	emitter := &capturingInternalEmitter{}
	lookups := 0
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: emitter, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		ReadySnapshots: func(context.Context, string) ([]trigger.TaskReadySnapshot, error) {
			return []trigger.TaskReadySnapshot{{
				TaskID: "TASK-911", Status: "open", IssueType: "task", SourceRepo: "acme/app", UpdatedAt: updatedAt,
			}}, nil
		},
		IssueLookup: func(context.Context, string, string) (trigger.TaskReadySnapshot, error) {
			lookups++
			return trigger.TaskReadySnapshot{
				TaskID: "TASK-911", Status: "open", IssueType: "task", SourceRepo: "acme/app", UpdatedAt: updatedAt,
			}, nil
		},
	}
	out, err := runTaskReadyBridge(t, bridge)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 1 || lookups != 1 {
		t.Fatalf("result/lookups = %+v/%d, want one synthetic emission and one generation lookup", out, lookups)
	}
	if events := emitter.eventsOfType(trigger.TaskReadyEventType); len(events) != 1 {
		t.Fatalf("task.ready emissions after snapshot plus release catch-up = %+v, want one", events)
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
	emitter := &capturingInternalEmitter{}
	lookups := 0
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: emitter, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		ReadySnapshots: func(context.Context, string) ([]trigger.TaskReadySnapshot, error) {
			return []trigger.TaskReadySnapshot{{
				TaskID: "TASK-912", Status: "open", IssueType: "task", SourceRepo: "acme/app", UpdatedAt: reconciledAt,
			}}, nil
		},
		IssueLookup: func(context.Context, string, string) (trigger.TaskReadySnapshot, error) {
			lookups++
			return trigger.TaskReadySnapshot{
				TaskID: "TASK-912", Status: "open", IssueType: "task", SourceRepo: "acme/app", UpdatedAt: releasedAt,
			}, nil
		},
	}
	out, err := runTaskReadyBridge(t, bridge)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.TaskReadyEmitted != 2 || lookups != 2 {
		t.Fatalf("result/lookups = %+v/%d, want synthetic plus newer release generation", out, lookups)
	}
	if events := emitter.eventsOfType(trigger.TaskReadyEventType); len(events) != 2 {
		t.Fatalf("task.ready emissions for distinct Ready generations = %+v, want two", events)
	}
}

func TestIssueJournalBridgeReconcileFailureRetries(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{"50": {next: "50"}}}
	cursors := newFixedCursorStore()
	cursors.Save("WS", "50")
	s := memstore.New()
	emitter := &capturingInternalEmitter{}
	calls := 0
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: emitter, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReady: true,
		ReadySnapshots: func(context.Context, string) ([]trigger.TaskReadySnapshot, error) {
			calls++
			if calls == 1 {
				return nil, errors.New("ready unavailable")
			}
			return []trigger.TaskReadySnapshot{{TaskID: "TASK-50", Status: "open", SourceRepo: "acme/app"}}, nil
		},
	}
	if _, err := runTaskReadyBridge(t, bridge); err == nil {
		t.Fatal("first RunOnce error = nil, want ready-list failure")
	}
	backedOff, err := runTaskReadyBridge(t, bridge)
	if err != nil {
		t.Fatalf("backoff RunOnce: %v", err)
	}
	if calls != 1 || backedOff.BackedOff != 1 {
		t.Fatalf("calls/result = %d/%+v, want one backoff pass", calls, backedOff)
	}
	out, err := runTaskReadyBridge(t, bridge)
	if err != nil {
		t.Fatalf("retry RunOnce: %v", err)
	}
	if calls != 2 || out.TaskReadyEmitted != 1 {
		t.Fatalf("calls/result = %d/%+v, want delayed retry and one emission", calls, out)
	}
}
