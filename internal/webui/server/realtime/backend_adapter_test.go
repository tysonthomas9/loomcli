package realtime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// sharedFixtureTimestamp is a deterministic instant used by every fixture
// in this file. Avoids time-dependent flakes and keeps the byte-equality
// assertions reproducible.
var sharedFixtureTimestamp = time.Date(2026, 4, 25, 10, 30, 45, 0, time.UTC)

func derefMutationAssignee(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// makeSharedFixture returns equivalent backend and realtime mutation values
// that represent the same logical event. Used to assert byte-level
// equivalence between BackendMutationToPayload and MutationEventToPayload.
func makeSharedFixture() (backend.MutationData, MutationEvent) {
	bm := backend.MutationData{
		Type:       "status",
		EntityType: "issue",
		EntityID:   "loom-fleet-42",
		Action:     "issue.close",
		IssueID:    "loom-fleet-42",
		Title:      "Implement SSE fleet subscriber",
		Assignee:   "agent-alpha",
		Actor:      "agent-beta",
		Timestamp:  sharedFixtureTimestamp,
		OldStatus:  "open",
		NewStatus:  "in_progress",
		ParentID:   "loom-epic-7",
		StepCount:  3,
		SourceRepo: "loomcli",
	}
	rm := MutationEvent{
		Type:       "status",
		EntityType: "issue",
		EntityID:   "loom-fleet-42",
		Action:     "issue.close",
		IssueID:    "loom-fleet-42",
		Title:      "Implement SSE fleet subscriber",
		Assignee:   "agent-alpha",
		Actor:      "agent-beta",
		Timestamp:  sharedFixtureTimestamp,
		OldStatus:  "open",
		NewStatus:  "in_progress",
		ParentID:   "loom-epic-7",
		StepCount:  3,
		SourceRepo: "loomcli",
	}
	return bm, rm
}

// TestBackendMutationToPayload_AllFields verifies every field is projected
// from backend.MutationData to MutationPayload, with workspaceID injected
// (since backend.MutationData has no WorkspaceID member).
func TestBackendMutationToPayload_AllFields(t *testing.T) {
	bm, _ := makeSharedFixture()
	got := BackendMutationToPayload(bm, "ws-fleet-1")

	checks := map[string]struct {
		actual, expected any
	}{
		"Type":        {got.Type, "status"},
		"EntityType":  {got.EntityType, "issue"},
		"EntityID":    {got.EntityID, "loom-fleet-42"},
		"Action":      {got.Action, "issue.close"},
		"IssueID":     {got.IssueID, "loom-fleet-42"},
		"Title":       {got.Title, "Implement SSE fleet subscriber"},
		"Assignee":    {derefMutationAssignee(got.Assignee), "agent-alpha"},
		"Actor":       {got.Actor, "agent-beta"},
		"OldStatus":   {got.OldStatus, "open"},
		"NewStatus":   {got.NewStatus, "in_progress"},
		"ParentID":    {got.ParentID, "loom-epic-7"},
		"StepCount":   {got.StepCount, 3},
		"SourceRepo":  {got.SourceRepo, "loomcli"},
		"WorkspaceID": {got.WorkspaceID, "ws-fleet-1"},
	}
	for name, c := range checks {
		if c.actual != c.expected {
			t.Errorf("%s = %v, want %v", name, c.actual, c.expected)
		}
	}

	// Timestamp is stringified; assert exact RFC3339 form (UTC).
	if got.Timestamp != "2026-04-25T10:30:45Z" {
		t.Errorf("Timestamp = %q, want %q", got.Timestamp, "2026-04-25T10:30:45Z")
	}
}

func TestBackendMutationToPayload_GenericAgentEvent(t *testing.T) {
	bm := backend.MutationData{
		Type:       "status",
		EntityType: "agent",
		EntityID:   "agent-alpha",
		Action:     "agent.status",
		Title:      "agent-alpha",
		Actor:      "tester",
		Timestamp:  sharedFixtureTimestamp,
	}
	got := BackendMutationToPayload(bm, "ws-agent-1")

	checks := map[string]struct {
		actual, expected any
	}{
		"Type":        {got.Type, "status"},
		"EntityType":  {got.EntityType, "agent"},
		"EntityID":    {got.EntityID, "agent-alpha"},
		"Action":      {got.Action, "agent.status"},
		"IssueID":     {got.IssueID, ""},
		"Title":       {got.Title, "agent-alpha"},
		"Actor":       {got.Actor, "tester"},
		"WorkspaceID": {got.WorkspaceID, "ws-agent-1"},
	}
	for name, c := range checks {
		if c.actual != c.expected {
			t.Errorf("%s = %v, want %v", name, c.actual, c.expected)
		}
	}
	if got.Timestamp != "2026-04-25T10:30:45Z" {
		t.Errorf("Timestamp = %q, want %q", got.Timestamp, "2026-04-25T10:30:45Z")
	}
}

// TestBackendMutationToPayload_TimestampNonUTC verifies that a
// non-UTC backend timestamp is normalized to UTC before formatting.
// Without this, two events emitted from different time zones would
// serialize differently even though they represent the same instant.
func TestBackendMutationToPayload_TimestampNonUTC(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("zoneinfo not available: %v", err)
	}
	// 2026-04-25T10:30:45Z is 2026-04-25T06:30:45-04:00
	bm := backend.MutationData{
		Type:      "create",
		IssueID:   "loom-1",
		Timestamp: sharedFixtureTimestamp.In(loc),
	}
	got := BackendMutationToPayload(bm, "ws-1")
	if got.Timestamp != "2026-04-25T10:30:45Z" {
		t.Errorf("non-UTC timestamp not normalized: got %q, want %q", got.Timestamp, "2026-04-25T10:30:45Z")
	}
}

func TestMutationPayloadPreservesSubsecondTimestamp(t *testing.T) {
	ts := time.Date(2026, 4, 25, 10, 30, 45, 987654321, time.UTC)
	want := "2026-04-25T10:30:45.987654321Z"

	backendPayload := BackendMutationToPayload(backend.MutationData{
		Type:      "status",
		IssueID:   "loom-1",
		Timestamp: ts,
	}, "ws-1")
	if backendPayload.Timestamp != want {
		t.Errorf("backend timestamp = %q, want %q", backendPayload.Timestamp, want)
	}

	eventPayload := MutationEventToPayload(MutationEvent{
		Type:      "status",
		IssueID:   "loom-1",
		Timestamp: ts,
	})
	if eventPayload.Timestamp != want {
		t.Errorf("event timestamp = %q, want %q", eventPayload.Timestamp, want)
	}
}

// TestBackendMutationToPayload_EmptyOptionalFields verifies that empty
// optional fields stay empty so omitempty produces minimal JSON, matching
// what MutationEventToPayload produces for a minimal event.
func TestBackendMutationToPayload_EmptyOptionalFields(t *testing.T) {
	bm := backend.MutationData{
		Type:      "create",
		IssueID:   "loom-7",
		Timestamp: sharedFixtureTimestamp,
	}
	got := BackendMutationToPayload(bm, "ws-min")

	if got.Title != "" || got.Assignee != nil || got.Actor != "" ||
		got.OldStatus != "" || got.NewStatus != "" || got.ParentID != "" ||
		got.StepCount != 0 || got.SourceRepo != "" {
		t.Errorf("optional fields should be zero-valued, got %+v", got)
	}
	if got.WorkspaceID != "ws-min" {
		t.Errorf("WorkspaceID = %q, want %q", got.WorkspaceID, "ws-min")
	}
}

func TestBackendMutationToPayload_AssigneeClearIsExplicit(t *testing.T) {
	got := BackendMutationToPayload(backend.MutationData{
		Type:       "update",
		EntityType: "issue",
		EntityID:   "loom-7",
		IssueID:    "loom-7",
		Action:     "issue.assign",
		Timestamp:  sharedFixtureTimestamp,
	}, "ws-min")

	if got.Assignee == nil || *got.Assignee != "" {
		t.Fatalf("issue.assign clear must preserve an explicit empty assignee, got %#v", got.Assignee)
	}
	wire, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if !strings.Contains(string(wire), `"assignee":""`) {
		t.Fatalf("assignee clear missing from wire payload: %s", wire)
	}
}

func TestBackendMutationToPayload_TaskRunTerminalHandoffClearsAssignee(t *testing.T) {
	got := BackendMutationToPayload(backend.MutationData{
		Type:       "update",
		EntityType: "issue",
		EntityID:   "loom-8",
		IssueID:    "loom-8",
		Action:     "issue.update",
		Actor:      "task-run:run-8",
		OldStatus:  "in_progress",
		NewStatus:  "review",
		Timestamp:  sharedFixtureTimestamp,
	}, "ws-min")

	if got.Assignee == nil || *got.Assignee != "" {
		t.Fatalf("task-run terminal handoff must preserve an explicit empty assignee, got %#v", got.Assignee)
	}
}

func TestBackendMutationToPayload_HumanStatusMoveDoesNotInventAssigneeClear(t *testing.T) {
	got := BackendMutationToPayload(backend.MutationData{
		Type:       "update",
		EntityType: "issue",
		EntityID:   "loom-9",
		IssueID:    "loom-9",
		Action:     "issue.update",
		Actor:      "user:tyson",
		OldStatus:  "in_progress",
		NewStatus:  "review",
		Timestamp:  sharedFixtureTimestamp,
	}, "ws-min")

	if got.Assignee != nil {
		t.Fatalf("human status move must not invent an assignee clear, got %#v", got.Assignee)
	}
}

// TestBackendMutationToPayload_WireFormatStability is the load-bearing
// assertion of this file: the JSON bytes produced by BackendMutationToPayload
// MUST equal the JSON bytes produced by MutationEventToPayload for the same
// logical event (after WorkspaceID is set on the rpc-derived payload by
// the SSE handler — we apply the same workspaceID by hand here).
//
// Any field-projection drift (renamed JSON tag, missing field, different
// time format, etc.) breaks reconnect catch-up where store-sourced and
// fleet-sourced events flow through the same SSE stream.
func TestBackendMutationToPayload_WireFormatStability(t *testing.T) {
	bm, rm := makeSharedFixture()
	const wsID = "ws-shared"

	backendPayload := BackendMutationToPayload(bm, wsID)
	eventPayload := MutationEventToPayload(rm)
	eventPayload.WorkspaceID = wsID // SSE handler injects this; mirror that here

	bBytes, err := json.Marshal(backendPayload)
	if err != nil {
		t.Fatalf("marshal backend payload: %v", err)
	}
	rBytes, err := json.Marshal(eventPayload)
	if err != nil {
		t.Fatalf("marshal event payload: %v", err)
	}
	if string(bBytes) != string(rBytes) {
		t.Errorf("wire-format drift between backend and event payloads:\n  backend: %s\n  event:   %s", bBytes, rBytes)
	}
}

// TestBackendMutationToEvent_RoundTripFields verifies that every field
// of backend.MutationData is preserved when projected to MutationEvent and
// back. The catch-up path projects backend to realtime; this test guards
// against losing fields silently when the two structs evolve.
func TestBackendMutationToEvent_RoundTripFields(t *testing.T) {
	original, _ := makeSharedFixture()

	event := BackendMutationToEvent(original)
	roundTripped := EventToMutationData(event)

	if !roundTripped.Timestamp.Equal(original.Timestamp) {
		t.Errorf("Timestamp lost equality: orig=%v rt=%v", original.Timestamp, roundTripped.Timestamp)
	}
	// Reset Timestamps to compare the rest with simple equality (time.Time
	// monotonic clock makes direct == unsafe even when Equal is true).
	roundTripped.Timestamp = original.Timestamp
	if roundTripped != original {
		t.Errorf("round-trip drift:\n  orig: %+v\n  got:  %+v", original, roundTripped)
	}
}

// TestEventToMutationData_AllFields verifies the inverse projection.
func TestEventToMutationData_AllFields(t *testing.T) {
	_, rm := makeSharedFixture()

	got := EventToMutationData(rm)
	if got.Type != rm.Type || got.EntityType != rm.EntityType ||
		got.EntityID != rm.EntityID || got.Action != rm.Action ||
		got.IssueID != rm.IssueID ||
		got.Title != rm.Title || got.Assignee != rm.Assignee ||
		got.Actor != rm.Actor || got.OldStatus != rm.OldStatus ||
		got.NewStatus != rm.NewStatus || got.ParentID != rm.ParentID ||
		got.StepCount != rm.StepCount || got.SourceRepo != rm.SourceRepo {
		t.Errorf("EventToMutationData field drift: got=%+v event=%+v", got, rm)
	}
	if !got.Timestamp.Equal(rm.Timestamp) {
		t.Errorf("Timestamp drift: got=%v event=%v", got.Timestamp, rm.Timestamp)
	}
}
