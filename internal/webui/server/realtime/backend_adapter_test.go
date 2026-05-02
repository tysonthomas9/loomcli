package realtime

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// sharedFixtureTimestamp is a deterministic instant used by every fixture
// in this file. Avoids time-dependent flakes and keeps the byte-equality
// assertions reproducible.
var sharedFixtureTimestamp = time.Date(2026, 4, 25, 10, 30, 45, 0, time.UTC)

// makeSharedFixture returns a (backend.MutationData, rpc.MutationEvent) pair
// that represent the same logical event. Used to assert byte-level
// equivalence between BackendMutationToPayload and RPCMutationToPayload.
func makeSharedFixture() (backend.MutationData, rpc.MutationEvent) {
	bm := backend.MutationData{
		Type:       "status",
		IssueID:    "bd-fleet-42",
		Title:      "Implement SSE fleet subscriber",
		Assignee:   "agent-alpha",
		Actor:      "agent-beta",
		Timestamp:  sharedFixtureTimestamp,
		OldStatus:  "open",
		NewStatus:  "in_progress",
		ParentID:   "bd-epic-7",
		StepCount:  3,
		SourceRepo: "loomcli",
	}
	rm := rpc.MutationEvent{
		Type:       "status",
		IssueID:    "bd-fleet-42",
		Title:      "Implement SSE fleet subscriber",
		Assignee:   "agent-alpha",
		Actor:      "agent-beta",
		Timestamp:  sharedFixtureTimestamp,
		OldStatus:  "open",
		NewStatus:  "in_progress",
		ParentID:   "bd-epic-7",
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
		"IssueID":     {got.IssueID, "bd-fleet-42"},
		"Title":       {got.Title, "Implement SSE fleet subscriber"},
		"Assignee":    {got.Assignee, "agent-alpha"},
		"Actor":       {got.Actor, "agent-beta"},
		"OldStatus":   {got.OldStatus, "open"},
		"NewStatus":   {got.NewStatus, "in_progress"},
		"ParentID":    {got.ParentID, "bd-epic-7"},
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
		IssueID:   "bd-1",
		Timestamp: sharedFixtureTimestamp.In(loc),
	}
	got := BackendMutationToPayload(bm, "ws-1")
	if got.Timestamp != "2026-04-25T10:30:45Z" {
		t.Errorf("non-UTC timestamp not normalized: got %q, want %q", got.Timestamp, "2026-04-25T10:30:45Z")
	}
}

// TestBackendMutationToPayload_EmptyOptionalFields verifies that empty
// optional fields stay empty so omitempty produces minimal JSON, matching
// what RPCMutationToPayload produces for a minimal event.
func TestBackendMutationToPayload_EmptyOptionalFields(t *testing.T) {
	bm := backend.MutationData{
		Type:      "create",
		IssueID:   "bd-7",
		Timestamp: sharedFixtureTimestamp,
	}
	got := BackendMutationToPayload(bm, "ws-min")

	if got.Title != "" || got.Assignee != "" || got.Actor != "" ||
		got.OldStatus != "" || got.NewStatus != "" || got.ParentID != "" ||
		got.StepCount != 0 || got.SourceRepo != "" {
		t.Errorf("optional fields should be zero-valued, got %+v", got)
	}
	if got.WorkspaceID != "ws-min" {
		t.Errorf("WorkspaceID = %q, want %q", got.WorkspaceID, "ws-min")
	}
}

// TestBackendMutationToPayload_WireFormatStability is the load-bearing
// assertion of this file: the JSON bytes produced by BackendMutationToPayload
// MUST equal the JSON bytes produced by RPCMutationToPayload for the same
// logical event (after WorkspaceID is set on the rpc-derived payload by
// the SSE handler — we apply the same workspaceID by hand here).
//
// Any field-projection drift (renamed JSON tag, missing field, different
// time format, etc.) breaks reconnect catch-up where beads-sourced and
// fleet-sourced events flow through the same SSE stream.
func TestBackendMutationToPayload_WireFormatStability(t *testing.T) {
	bm, rm := makeSharedFixture()
	const wsID = "ws-shared"

	backendPayload := BackendMutationToPayload(bm, wsID)
	rpcPayload := RPCMutationToPayload(rm)
	rpcPayload.WorkspaceID = wsID // SSE handler injects this; mirror that here

	bBytes, err := json.Marshal(backendPayload)
	if err != nil {
		t.Fatalf("marshal backend payload: %v", err)
	}
	rBytes, err := json.Marshal(rpcPayload)
	if err != nil {
		t.Fatalf("marshal rpc payload: %v", err)
	}
	if string(bBytes) != string(rBytes) {
		t.Errorf("wire-format drift between backend and rpc payloads:\n  backend: %s\n  rpc:     %s", bBytes, rBytes)
	}
}

// TestBackendMutationToRPCEvent_RoundTripFields verifies that every field
// of backend.MutationData is preserved when projected to rpc.MutationEvent
// and back. The catch-up path projects backend → rpc; this test guards
// against losing fields silently when the two structs evolve.
func TestBackendMutationToRPCEvent_RoundTripFields(t *testing.T) {
	original, _ := makeSharedFixture()

	rpcEvt := BackendMutationToRPCEvent(original)
	roundTripped := RPCEventToMutationData(rpcEvt)

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

// TestRPCEventToMutationData_AllFields verifies the inverse projection.
func TestRPCEventToMutationData_AllFields(t *testing.T) {
	_, rm := makeSharedFixture()

	got := RPCEventToMutationData(rm)
	if got.Type != rm.Type || got.IssueID != rm.IssueID ||
		got.Title != rm.Title || got.Assignee != rm.Assignee ||
		got.Actor != rm.Actor || got.OldStatus != rm.OldStatus ||
		got.NewStatus != rm.NewStatus || got.ParentID != rm.ParentID ||
		got.StepCount != rm.StepCount || got.SourceRepo != rm.SourceRepo {
		t.Errorf("RPCEventToMutationData field drift: got=%+v rpc=%+v", got, rm)
	}
	if !got.Timestamp.Equal(rm.Timestamp) {
		t.Errorf("Timestamp drift: got=%v rpc=%v", got.Timestamp, rm.Timestamp)
	}
}
