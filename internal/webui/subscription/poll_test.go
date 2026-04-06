package subscription

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// --- Tests for parseChangedIssues ---

// TestParseChangedIssues_ValidJSON tests that parseChangedIssues correctly parses valid JSON.
func TestParseChangedIssues_ValidJSON(t *testing.T) {
	issues := []struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		Assignee   string `json:"assignee"`
		Status     string `json:"status"`
		Priority   int    `json:"priority"`
		SourceRepo string `json:"source_repo"`
	}{
		{ID: "bd-1", Title: "First", Assignee: "alice", Status: "open", Priority: 1, SourceRepo: "repo-a"},
		{ID: "bd-2", Title: "Second", Assignee: "bob", Status: "in_progress", Priority: 2, SourceRepo: "repo-b"},
	}
	data, err := json.Marshal(issues)
	if err != nil {
		t.Fatalf("failed to marshal test data: %v", err)
	}

	result := parseChangedIssues(json.RawMessage(data))
	if result == nil {
		t.Fatal("parseChangedIssues returned nil for valid JSON")
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(result))
	}

	// Verify first issue
	if result[0].ID != "bd-1" {
		t.Errorf("result[0].ID = %q, want %q", result[0].ID, "bd-1")
	}
	if result[0].Title != "First" {
		t.Errorf("result[0].Title = %q, want %q", result[0].Title, "First")
	}
	if result[0].Assignee != "alice" {
		t.Errorf("result[0].Assignee = %q, want %q", result[0].Assignee, "alice")
	}
	if result[0].Status != "open" {
		t.Errorf("result[0].Status = %q, want %q", result[0].Status, "open")
	}
	if result[0].Priority != 1 {
		t.Errorf("result[0].Priority = %d, want %d", result[0].Priority, 1)
	}
	if result[0].SourceRepo != "repo-a" {
		t.Errorf("result[0].SourceRepo = %q, want %q", result[0].SourceRepo, "repo-a")
	}

	// Verify second issue
	if result[1].ID != "bd-2" {
		t.Errorf("result[1].ID = %q, want %q", result[1].ID, "bd-2")
	}
	if result[1].Status != "in_progress" {
		t.Errorf("result[1].Status = %q, want %q", result[1].Status, "in_progress")
	}
}

// TestParseChangedIssues_EmptyJSON tests parseChangedIssues with an empty JSON array.
func TestParseChangedIssues_EmptyJSON(t *testing.T) {
	data := json.RawMessage(`[]`)
	result := parseChangedIssues(data)
	if result == nil {
		t.Fatal("parseChangedIssues returned nil for empty array")
	}
	if len(result) != 0 {
		t.Errorf("expected 0 issues, got %d", len(result))
	}
}

// TestParseChangedIssues_InvalidJSON tests parseChangedIssues with invalid JSON.
func TestParseChangedIssues_InvalidJSON(t *testing.T) {
	data := json.RawMessage(`not valid json at all`)
	result := parseChangedIssues(data)
	if result != nil {
		t.Errorf("expected nil for invalid JSON, got %v", result)
	}
}

// TestParseChangedIssues_NullJSON tests parseChangedIssues with JSON null.
// JSON null unmarshals into an empty slice (not nil) because the struct array is
// initialized by json.Unmarshal — the function then returns an empty result slice.
func TestParseChangedIssues_NullJSON(t *testing.T) {
	data := json.RawMessage(`null`)
	result := parseChangedIssues(data)
	if result == nil {
		t.Fatal("expected non-nil empty slice for null JSON")
	}
	if len(result) != 0 {
		t.Errorf("expected 0 issues for null JSON, got %d", len(result))
	}
}

// TestParseChangedIssues_PartialFields tests parseChangedIssues with issues that have only some fields.
func TestParseChangedIssues_PartialFields(t *testing.T) {
	data := json.RawMessage(`[{"id":"bd-1","title":"Partial"}]`)
	result := parseChangedIssues(data)
	if result == nil {
		t.Fatal("parseChangedIssues returned nil")
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(result))
	}
	if result[0].ID != "bd-1" {
		t.Errorf("result[0].ID = %q, want %q", result[0].ID, "bd-1")
	}
	if result[0].Assignee != "" {
		t.Errorf("result[0].Assignee = %q, want empty", result[0].Assignee)
	}
	if result[0].Priority != 0 {
		t.Errorf("result[0].Priority = %d, want 0", result[0].Priority)
	}
}

// TestParseChangedIssues_ExtraFieldsIgnored tests that extra/unknown JSON fields are silently ignored.
func TestParseChangedIssues_ExtraFieldsIgnored(t *testing.T) {
	data := json.RawMessage(`[{"id":"bd-1","title":"Extra","unknown_field":"ignored","count":42}]`)
	result := parseChangedIssues(data)
	if result == nil {
		t.Fatal("parseChangedIssues returned nil")
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(result))
	}
	if result[0].ID != "bd-1" {
		t.Errorf("result[0].ID = %q, want %q", result[0].ID, "bd-1")
	}
}

// --- Tests for emitGranularMutations ---

// TestEmitGranularMutations_NewIssue tests that emitGranularMutations broadcasts MutationCreate
// for an issue not present in knownIssues.
func TestEmitGranularMutations_NewIssue(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(nil, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.knownIssues = make(map[string]knownIssueState) // empty

	now := time.Now()
	changed := []changedIssue{
		{ID: "bd-new-1", Title: "New Issue", Assignee: "alice", Status: "open", Priority: 1, SourceRepo: "repo-a"},
	}

	subscriber.emitGranularMutations(changed, now, 10)

	// Should receive a MutationCreate event
	select {
	case received := <-client.Send():
		if received.Type != rpc.MutationCreate {
			t.Errorf("expected type %q, got %q", rpc.MutationCreate, received.Type)
		}
		if received.IssueID != "bd-new-1" {
			t.Errorf("expected issue_id %q, got %q", "bd-new-1", received.IssueID)
		}
		if received.NewStatus != "open" {
			t.Errorf("expected new_status %q, got %q", "open", received.NewStatus)
		}
		if received.Title != "New Issue" {
			t.Errorf("expected title %q, got %q", "New Issue", received.Title)
		}
		if received.Priority == nil || *received.Priority != 1 {
			t.Errorf("expected priority 1, got %v", received.Priority)
		}
		if received.WorkspaceID != "test-ws" {
			t.Errorf("expected workspace_id %q, got %q", "test-ws", received.WorkspaceID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("did not receive MutationCreate broadcast")
	}

	// Verify knownIssues was updated
	subscriber.mu.RLock()
	state, exists := subscriber.knownIssues["bd-new-1"]
	subscriber.mu.RUnlock()
	if !exists {
		t.Error("expected knownIssues to contain bd-new-1 after emitGranularMutations")
	}
	if state.Title != "New Issue" {
		t.Errorf("knownIssues[bd-new-1].Title = %q, want %q", state.Title, "New Issue")
	}
}

// TestEmitGranularMutations_StatusChange tests that emitGranularMutations broadcasts MutationStatus
// when the issue's status has changed.
func TestEmitGranularMutations_StatusChange(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(nil, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.knownIssues = map[string]knownIssueState{
		"bd-1": {Title: "Issue One", Assignee: "alice", Status: "open", Priority: 2},
	}

	now := time.Now()
	changed := []changedIssue{
		{ID: "bd-1", Title: "Issue One", Assignee: "alice", Status: "in_progress", Priority: 2, SourceRepo: "repo-a"},
	}

	subscriber.emitGranularMutations(changed, now, 10)

	select {
	case received := <-client.Send():
		if received.Type != rpc.MutationStatus {
			t.Errorf("expected type %q, got %q", rpc.MutationStatus, received.Type)
		}
		if received.OldStatus != "open" {
			t.Errorf("expected old_status %q, got %q", "open", received.OldStatus)
		}
		if received.NewStatus != "in_progress" {
			t.Errorf("expected new_status %q, got %q", "in_progress", received.NewStatus)
		}
		if received.IssueID != "bd-1" {
			t.Errorf("expected issue_id %q, got %q", "bd-1", received.IssueID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("did not receive MutationStatus broadcast")
	}

	// Verify knownIssues was updated with new status
	subscriber.mu.RLock()
	state := subscriber.knownIssues["bd-1"]
	subscriber.mu.RUnlock()
	if state.Status != "in_progress" {
		t.Errorf("knownIssues[bd-1].Status = %q, want %q", state.Status, "in_progress")
	}
}

// TestEmitGranularMutations_UpdateNonStatus tests that emitGranularMutations broadcasts MutationUpdate
// when the issue exists in knownIssues but the status is unchanged.
func TestEmitGranularMutations_UpdateNonStatus(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(nil, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.knownIssues = map[string]knownIssueState{
		"bd-1": {Title: "Old Title", Assignee: "alice", Status: "open", Priority: 2},
	}

	now := time.Now()
	changed := []changedIssue{
		{ID: "bd-1", Title: "New Title", Assignee: "bob", Status: "open", Priority: 1, SourceRepo: "repo-a"},
	}

	subscriber.emitGranularMutations(changed, now, 10)

	select {
	case received := <-client.Send():
		if received.Type != rpc.MutationUpdate {
			t.Errorf("expected type %q, got %q", rpc.MutationUpdate, received.Type)
		}
		if received.IssueID != "bd-1" {
			t.Errorf("expected issue_id %q, got %q", "bd-1", received.IssueID)
		}
		if received.Title != "New Title" {
			t.Errorf("expected title %q, got %q", "New Title", received.Title)
		}
		if received.Assignee != "bob" {
			t.Errorf("expected assignee %q, got %q", "bob", received.Assignee)
		}
		if received.Priority == nil || *received.Priority != 1 {
			t.Errorf("expected priority 1, got %v", received.Priority)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("did not receive MutationUpdate broadcast")
	}
}

// TestEmitGranularMutations_SkipsEmptyID tests that emitGranularMutations skips issues with empty IDs.
func TestEmitGranularMutations_SkipsEmptyID(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(nil, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.knownIssues = make(map[string]knownIssueState)

	now := time.Now()
	changed := []changedIssue{
		{ID: "", Title: "No ID Issue", Status: "open"},
	}

	subscriber.emitGranularMutations(changed, now, 5)

	// Should NOT receive any broadcast
	select {
	case received := <-client.Send():
		t.Errorf("expected no broadcast for empty-ID issue, but received: %+v", received)
	case <-time.After(200 * time.Millisecond):
		// Good — no broadcast
	}

	// Verify knownIssues does not contain empty key
	subscriber.mu.RLock()
	_, exists := subscriber.knownIssues[""]
	subscriber.mu.RUnlock()
	if exists {
		t.Error("knownIssues should not contain an entry with empty key")
	}
}

// TestEmitGranularMutations_UpdatesTrackingState tests that emitGranularMutations updates
// lastKnownCount and lastPollTime.
func TestEmitGranularMutations_UpdatesTrackingState(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	// No client needed — we just check tracking state
	subscriber := NewDaemonSubscriber(nil, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.knownIssues = make(map[string]knownIssueState)
	subscriber.lastKnownCount = 5
	oldPollTime := time.Now().Add(-10 * time.Second)
	subscriber.lastPollTime = oldPollTime

	now := time.Now()
	subscriber.emitGranularMutations([]changedIssue{}, now, 42)

	subscriber.mu.RLock()
	defer subscriber.mu.RUnlock()
	if subscriber.lastKnownCount != 42 {
		t.Errorf("lastKnownCount = %d, want 42", subscriber.lastKnownCount)
	}
	if subscriber.lastPollTime != now {
		t.Errorf("lastPollTime was not updated to now")
	}
}

// TestEmitGranularMutations_MixedMutationTypes tests emitGranularMutations with a mix of
// new, status-changed, and update issues in a single batch.
func TestEmitGranularMutations_MixedMutationTypes(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(nil, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.knownIssues = map[string]knownIssueState{
		"bd-existing": {Title: "Existing", Assignee: "alice", Status: "open", Priority: 2},
		"bd-status":   {Title: "Status Change", Assignee: "bob", Status: "open", Priority: 1},
	}

	now := time.Now()
	changed := []changedIssue{
		{ID: "bd-new", Title: "Brand New", Status: "open", Priority: 3},              // new → MutationCreate
		{ID: "bd-existing", Title: "Updated", Assignee: "alice", Status: "open"},     // same status → MutationUpdate
		{ID: "bd-status", Title: "Status Change", Assignee: "bob", Status: "closed"}, // status changed → MutationStatus
	}

	subscriber.emitGranularMutations(changed, now, 15)

	// Collect 3 broadcasts
	received := make(map[string]*realtime.MutationPayload, 3)
	timeout := time.After(1 * time.Second)
	for i := 0; i < 3; i++ {
		select {
		case msg := <-client.Send():
			received[msg.IssueID] = msg
		case <-timeout:
			t.Fatalf("timed out waiting for broadcast %d/3", i+1)
		}
	}

	// Verify types
	if received["bd-new"] == nil || received["bd-new"].Type != rpc.MutationCreate {
		t.Errorf("bd-new: expected MutationCreate, got %v", received["bd-new"])
	}
	if received["bd-existing"] == nil || received["bd-existing"].Type != rpc.MutationUpdate {
		t.Errorf("bd-existing: expected MutationUpdate, got %v", received["bd-existing"])
	}
	if received["bd-status"] == nil || received["bd-status"].Type != rpc.MutationStatus {
		t.Errorf("bd-status: expected MutationStatus, got %v", received["bd-status"])
	}
	if received["bd-status"] != nil && received["bd-status"].OldStatus != "open" {
		t.Errorf("bd-status: expected old_status %q, got %q", "open", received["bd-status"].OldStatus)
	}
}

// --- Tests for broadcastRefresh ---

// TestBroadcastRefresh tests that broadcastRefresh sends a MutationRefresh event
// and updates poll state.
func TestBroadcastRefresh(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(nil, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.lastKnownCount = 5
	oldPollTime := time.Now().Add(-10 * time.Second)
	subscriber.lastPollTime = oldPollTime

	now := time.Now()
	subscriber.broadcastRefresh(now, 42)

	// Should receive MutationRefresh
	select {
	case received := <-client.Send():
		if received.Type != rpc.MutationRefresh {
			t.Errorf("expected type %q, got %q", rpc.MutationRefresh, received.Type)
		}
		if received.IssueID != "" {
			t.Errorf("expected empty issue_id for refresh, got %q", received.IssueID)
		}
		if received.WorkspaceID != "test-ws" {
			t.Errorf("expected workspace_id %q, got %q", "test-ws", received.WorkspaceID)
		}
		if received.Timestamp == "" {
			t.Error("expected non-empty timestamp")
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("did not receive MutationRefresh broadcast")
	}

	// Verify poll state was updated
	subscriber.mu.RLock()
	defer subscriber.mu.RUnlock()
	if subscriber.lastKnownCount != 42 {
		t.Errorf("lastKnownCount = %d, want 42", subscriber.lastKnownCount)
	}
	if subscriber.lastPollTime != now {
		t.Errorf("lastPollTime was not updated")
	}
}

// --- Tests for pollDBChanges granular path ---

// TestPollDBChanges_GranularPath_EmitsIndividualMutations tests the full granular path:
// count unchanged + updates detected → List → individual MutationUpdate/MutationCreate/MutationStatus events.
func TestPollDBChanges_GranularPath_EmitsIndividualMutations(t *testing.T) {
	callNumber := int32(0)
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "count":
			n := atomic.AddInt32(&callNumber, 1)
			if n == 1 {
				// First count call: return 10 (same as lastKnownCount)
				countData, _ := json.Marshal(struct {
					Count int64 `json:"count"`
				}{Count: 10})
				return rpc.Response{Success: true, Data: countData}
			}
			// Second count call (UpdatedAfter check): return 2 updated issues
			countData, _ := json.Marshal(struct {
				Count int64 `json:"count"`
			}{Count: 2})
			return rpc.Response{Success: true, Data: countData}
		case "list":
			// Return 2 changed issues
			issues := []struct {
				ID         string `json:"id"`
				Title      string `json:"title"`
				Assignee   string `json:"assignee"`
				Status     string `json:"status"`
				Priority   int    `json:"priority"`
				SourceRepo string `json:"source_repo"`
			}{
				{ID: "bd-1", Title: "Updated Title", Assignee: "alice", Status: "open", Priority: 1, SourceRepo: "repo-a"},
				{ID: "bd-new", Title: "New Issue", Assignee: "bob", Status: "in_progress", Priority: 2, SourceRepo: "repo-a"},
			}
			data, _ := json.Marshal(issues)
			return rpc.Response{Success: true, Data: data}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 10 // Same as server will return — no count change
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)
	// Pre-populate knownIssues: bd-1 exists (MutationUpdate), bd-new does not (MutationCreate)
	subscriber.knownIssues = map[string]knownIssueState{
		"bd-1": {Title: "Old Title", Assignee: "alice", Status: "open", Priority: 2},
	}

	subscriber.pollDBChanges()

	// Collect 2 granular broadcasts (NOT a MutationRefresh)
	received := make(map[string]*realtime.MutationPayload, 2)
	timeout := time.After(1 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case msg := <-client.Send():
			received[msg.IssueID] = msg
		case <-timeout:
			t.Fatalf("timed out waiting for broadcast %d/2", i+1)
		}
	}

	// bd-1: should be MutationUpdate (existed in knownIssues, status unchanged)
	if received["bd-1"] == nil {
		t.Fatal("expected broadcast for bd-1")
	}
	if received["bd-1"].Type != rpc.MutationUpdate {
		t.Errorf("bd-1: expected type %q, got %q", rpc.MutationUpdate, received["bd-1"].Type)
	}
	if received["bd-1"].Title != "Updated Title" {
		t.Errorf("bd-1: expected title %q, got %q", "Updated Title", received["bd-1"].Title)
	}

	// bd-new: should be MutationCreate (not in knownIssues)
	if received["bd-new"] == nil {
		t.Fatal("expected broadcast for bd-new")
	}
	if received["bd-new"].Type != rpc.MutationCreate {
		t.Errorf("bd-new: expected type %q, got %q", rpc.MutationCreate, received["bd-new"].Type)
	}
	if received["bd-new"].NewStatus != "in_progress" {
		t.Errorf("bd-new: expected new_status %q, got %q", "in_progress", received["bd-new"].NewStatus)
	}

	// Ensure no MutationRefresh was sent
	select {
	case extra := <-client.Send():
		if extra.Type == rpc.MutationRefresh {
			t.Errorf("did not expect MutationRefresh in granular path, but received one")
		}
	case <-time.After(200 * time.Millisecond):
		// Good — no extra broadcasts
	}
}

// TestPollDBChanges_FallbackOnDeletion tests that pollDBChanges falls back to MutationRefresh
// when the count decreases (deletion detected).
func TestPollDBChanges_FallbackOnDeletion(t *testing.T) {
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "count":
			// Return count of 8 (less than lastKnownCount of 10 → deletion)
			countData, _ := json.Marshal(struct {
				Count int64 `json:"count"`
			}{Count: 8})
			return rpc.Response{Success: true, Data: countData}
		case "list":
			// loadKnownIssues will call List — return empty for simplicity
			data, _ := json.Marshal([]struct{}{})
			return rpc.Response{Success: true, Data: data}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 10 // Server returns 8 → deletion detected
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)
	subscriber.knownIssues = map[string]knownIssueState{
		"bd-1": {Title: "Issue One", Status: "open"},
	}

	subscriber.pollDBChanges()

	// Should receive MutationRefresh (not granular mutations)
	select {
	case received := <-client.Send():
		if received.Type != rpc.MutationRefresh {
			t.Errorf("expected type %q on deletion, got %q", rpc.MutationRefresh, received.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("did not receive MutationRefresh broadcast after deletion")
	}

	// Verify lastKnownCount was updated to 8
	subscriber.mu.RLock()
	count := subscriber.lastKnownCount
	subscriber.mu.RUnlock()
	if count != 8 {
		t.Errorf("lastKnownCount = %d, want 8", count)
	}
}

// TestPollDBChanges_FallbackOnThresholdExceeded tests that pollDBChanges falls back to
// MutationRefresh when more than granularMutationThreshold issues changed.
func TestPollDBChanges_FallbackOnThresholdExceeded(t *testing.T) {
	callNumber := int32(0)

	// Build a list of 101 issues (exceeds threshold of 100)
	type simpleIssue struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Status string `json:"status"`
	}
	manyIssues := make([]simpleIssue, granularMutationThreshold+1)
	for i := range manyIssues {
		manyIssues[i] = simpleIssue{
			ID:     "bd-" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Title:  "Issue",
			Status: "open",
		}
	}
	manyIssuesData, _ := json.Marshal(manyIssues)

	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "count":
			n := atomic.AddInt32(&callNumber, 1)
			if n == 1 {
				// Same count → no count change
				countData, _ := json.Marshal(struct {
					Count int64 `json:"count"`
				}{Count: 10})
				return rpc.Response{Success: true, Data: countData}
			}
			// UpdatedAfter: lots of updated issues
			countData, _ := json.Marshal(struct {
				Count int64 `json:"count"`
			}{Count: int64(granularMutationThreshold + 1)})
			return rpc.Response{Success: true, Data: countData}
		case "list":
			// Check if this is a loadKnownIssues call (has limit in args) or
			// a fetchChangedIssues call (has updated_after in args)
			var args struct {
				UpdatedAfter string `json:"updated_after"`
				Limit        int    `json:"limit"`
			}
			if req.Args != nil {
				json.Unmarshal(req.Args, &args)
			}
			if args.Limit > 0 {
				// loadKnownIssues call — return empty
				return rpc.Response{Success: true, Data: []byte("[]")}
			}
			// fetchChangedIssues — return >100 issues
			return rpc.Response{Success: true, Data: manyIssuesData}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 256, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 10
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)
	subscriber.knownIssues = make(map[string]knownIssueState)

	subscriber.pollDBChanges()

	// Should receive MutationRefresh (threshold exceeded)
	select {
	case received := <-client.Send():
		if received.Type != rpc.MutationRefresh {
			t.Errorf("expected type %q when threshold exceeded, got %q", rpc.MutationRefresh, received.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("did not receive MutationRefresh broadcast after threshold exceeded")
	}
}

// TestPollDBChanges_FallbackWhenListFails tests that pollDBChanges falls back to MutationRefresh
// when the List RPC call fails.
func TestPollDBChanges_FallbackWhenListFails(t *testing.T) {
	callNumber := int32(0)
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "count":
			n := atomic.AddInt32(&callNumber, 1)
			if n == 1 {
				countData, _ := json.Marshal(struct {
					Count int64 `json:"count"`
				}{Count: 10})
				return rpc.Response{Success: true, Data: countData}
			}
			// UpdatedAfter: 1 updated issue
			countData, _ := json.Marshal(struct {
				Count int64 `json:"count"`
			}{Count: 1})
			return rpc.Response{Success: true, Data: countData}
		case "list":
			// List fails
			return rpc.Response{Success: false, Error: "database locked"}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 10
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)
	subscriber.knownIssues = make(map[string]knownIssueState)

	subscriber.pollDBChanges()

	// Should fall back to MutationRefresh
	select {
	case received := <-client.Send():
		if received.Type != rpc.MutationRefresh {
			t.Errorf("expected type %q when List fails, got %q", rpc.MutationRefresh, received.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("did not receive MutationRefresh broadcast when List RPC fails")
	}
}

// TestPollDBChanges_GranularPath_StatusChange tests the granular path when an issue's
// status changes — should emit MutationStatus.
func TestPollDBChanges_GranularPath_StatusChange(t *testing.T) {
	callNumber := int32(0)
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "count":
			n := atomic.AddInt32(&callNumber, 1)
			if n == 1 {
				countData, _ := json.Marshal(struct {
					Count int64 `json:"count"`
				}{Count: 10})
				return rpc.Response{Success: true, Data: countData}
			}
			// UpdatedAfter: 1 updated issue
			countData, _ := json.Marshal(struct {
				Count int64 `json:"count"`
			}{Count: 1})
			return rpc.Response{Success: true, Data: countData}
		case "list":
			issues := []struct {
				ID     string `json:"id"`
				Title  string `json:"title"`
				Status string `json:"status"`
			}{
				{ID: "bd-1", Title: "Issue One", Status: "completed"},
			}
			data, _ := json.Marshal(issues)
			return rpc.Response{Success: true, Data: data}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 10
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)
	subscriber.knownIssues = map[string]knownIssueState{
		"bd-1": {Title: "Issue One", Status: "open"}, // Status was "open", now "completed"
	}

	subscriber.pollDBChanges()

	select {
	case received := <-client.Send():
		if received.Type != rpc.MutationStatus {
			t.Errorf("expected type %q, got %q", rpc.MutationStatus, received.Type)
		}
		if received.OldStatus != "open" {
			t.Errorf("expected old_status %q, got %q", "open", received.OldStatus)
		}
		if received.NewStatus != "completed" {
			t.Errorf("expected new_status %q, got %q", "completed", received.NewStatus)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("did not receive MutationStatus broadcast")
	}
}

// TestPollDBChanges_CountIncrease_GranularPath tests that when count increases (new issues),
// the granular path is used (not fallback) since count increase is not deletion.
func TestPollDBChanges_CountIncrease_GranularPath(t *testing.T) {
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "count":
			// Return count of 12 (increased from lastKnownCount of 10)
			countData, _ := json.Marshal(struct {
				Count int64 `json:"count"`
			}{Count: 12})
			return rpc.Response{Success: true, Data: countData}
		case "list":
			issues := []struct {
				ID     string `json:"id"`
				Title  string `json:"title"`
				Status string `json:"status"`
			}{
				{ID: "bd-new-1", Title: "New One", Status: "open"},
				{ID: "bd-new-2", Title: "New Two", Status: "open"},
			}
			data, _ := json.Marshal(issues)
			return rpc.Response{Success: true, Data: data}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, 64, 0, nil, "test-ws")
	hub.RegisterClient(client)
	time.Sleep(50 * time.Millisecond)

	subscriber := NewDaemonSubscriber(pool, hub)
	subscriber.workspaceID = "test-ws"
	subscriber.countInitialized = true
	subscriber.lastKnownCount = 10
	subscriber.lastPollTime = time.Now().Add(-5 * time.Second)
	subscriber.knownIssues = make(map[string]knownIssueState)

	subscriber.pollDBChanges()

	// Collect 2 granular broadcasts (MutationCreate for new issues)
	received := make(map[string]*realtime.MutationPayload, 2)
	timeout := time.After(1 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case msg := <-client.Send():
			received[msg.IssueID] = msg
		case <-timeout:
			t.Fatalf("timed out waiting for broadcast %d/2", i+1)
		}
	}

	if received["bd-new-1"] == nil || received["bd-new-1"].Type != rpc.MutationCreate {
		t.Errorf("bd-new-1: expected MutationCreate, got %v", received["bd-new-1"])
	}
	if received["bd-new-2"] == nil || received["bd-new-2"].Type != rpc.MutationCreate {
		t.Errorf("bd-new-2: expected MutationCreate, got %v", received["bd-new-2"])
	}

	// Verify lastKnownCount was updated to 12
	subscriber.mu.RLock()
	count := subscriber.lastKnownCount
	subscriber.mu.RUnlock()
	if count != 12 {
		t.Errorf("lastKnownCount = %d, want 12", count)
	}
}

// TestGranularMutationThreshold tests that the constant is set as expected.
func TestGranularMutationThreshold(t *testing.T) {
	if granularMutationThreshold != 100 {
		t.Errorf("granularMutationThreshold = %d, want 100", granularMutationThreshold)
	}
}

// TestLoadKnownIssues tests that loadKnownIssues populates the knownIssues map from the server.
func TestLoadKnownIssues(t *testing.T) {
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "list":
			issues := []struct {
				ID       string `json:"id"`
				Title    string `json:"title"`
				Assignee string `json:"assignee"`
				Status   string `json:"status"`
				Priority int    `json:"priority"`
			}{
				{ID: "bd-1", Title: "First", Assignee: "alice", Status: "open", Priority: 1},
				{ID: "bd-2", Title: "Second", Assignee: "bob", Status: "closed", Priority: 3},
			}
			data, _ := json.Marshal(issues)
			return rpc.Response{Success: true, Data: data}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	subscriber := NewDaemonSubscriber(pool, hub)

	// Load known issues
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := pool.Get(ctx)
	if err != nil {
		t.Fatalf("failed to get client: %v", err)
	}
	defer pool.Put(client)

	subscriber.loadKnownIssues(client)

	subscriber.mu.RLock()
	defer subscriber.mu.RUnlock()

	if len(subscriber.knownIssues) != 2 {
		t.Fatalf("expected 2 known issues, got %d", len(subscriber.knownIssues))
	}

	state1, ok := subscriber.knownIssues["bd-1"]
	if !ok {
		t.Fatal("expected bd-1 in knownIssues")
	}
	if state1.Title != "First" || state1.Assignee != "alice" || state1.Status != "open" || state1.Priority != 1 {
		t.Errorf("bd-1 state mismatch: %+v", state1)
	}

	state2, ok := subscriber.knownIssues["bd-2"]
	if !ok {
		t.Fatal("expected bd-2 in knownIssues")
	}
	if state2.Title != "Second" || state2.Status != "closed" {
		t.Errorf("bd-2 state mismatch: %+v", state2)
	}
}

// TestLoadKnownIssues_SkipsEmptyID tests that loadKnownIssues skips issues with empty IDs.
func TestLoadKnownIssues_SkipsEmptyID(t *testing.T) {
	socketPath := startSubscriptionMockServerRaw(t, func(req rpc.Request) rpc.Response {
		switch req.Operation {
		case "health":
			hd, _ := json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "0.0.0", Compatible: true})
			return rpc.Response{Success: true, Data: hd}
		case "ping":
			return rpc.Response{Success: true}
		case "list":
			data := json.RawMessage(`[{"id":"","title":"No ID"},{"id":"bd-1","title":"Has ID"}]`)
			return rpc.Response{Success: true, Data: data}
		default:
			return rpc.Response{Success: false, Error: "unknown operation: " + req.Operation}
		}
	})

	pool := newSubscriptionMockPool(socketPath)
	defer pool.Close()

	hub := realtime.NewHub()
	subscriber := NewDaemonSubscriber(pool, hub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := pool.Get(ctx)
	if err != nil {
		t.Fatalf("failed to get client: %v", err)
	}
	defer pool.Put(client)

	subscriber.loadKnownIssues(client)

	subscriber.mu.RLock()
	defer subscriber.mu.RUnlock()

	if len(subscriber.knownIssues) != 1 {
		t.Errorf("expected 1 known issue (empty ID skipped), got %d", len(subscriber.knownIssues))
	}
	if _, ok := subscriber.knownIssues[""]; ok {
		t.Error("knownIssues should not contain empty ID entry")
	}
}

// TestMutationPayload_PriorityField tests that realtime.MutationPayload correctly serializes/deserializes
// the Priority field as a pointer.
func TestMutationPayload_PriorityField(t *testing.T) {
	prio := 3
	payload := &realtime.MutationPayload{
		Type:     rpc.MutationUpdate,
		IssueID:  "bd-1",
		Priority: &prio,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded realtime.MutationPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Priority == nil {
		t.Fatal("expected non-nil priority after roundtrip")
	}
	if *decoded.Priority != 3 {
		t.Errorf("priority = %d, want 3", *decoded.Priority)
	}

	// Test nil priority is omitted
	payloadNoPrio := &realtime.MutationPayload{
		Type:    rpc.MutationUpdate,
		IssueID: "bd-2",
	}
	data2, _ := json.Marshal(payloadNoPrio)
	var decoded2 realtime.MutationPayload
	json.Unmarshal(data2, &decoded2)

	if decoded2.Priority != nil {
		t.Errorf("expected nil priority when not set, got %v", decoded2.Priority)
	}
}
