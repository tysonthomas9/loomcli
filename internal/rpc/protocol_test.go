package rpc

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRequest_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := Request{
		Operation:     OpCreate,
		Args:          json.RawMessage(`{"title":"test"}`),
		Actor:         "alice",
		RequestID:     "req-123",
		Cwd:           "/home/user/project",
		ClientVersion: "1.0.0",
		ExpectedDB:    "/home/user/project/.loom/db.sqlite",
	}

	data, err := json.Marshal(original) // #nosec G117 — test struct, no real secrets
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	var restored Request
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}

	if restored.Operation != original.Operation {
		t.Errorf("Operation = %q, want %q", restored.Operation, original.Operation)
	}
	if restored.Actor != original.Actor {
		t.Errorf("Actor = %q, want %q", restored.Actor, original.Actor)
	}
	if restored.RequestID != original.RequestID {
		t.Errorf("RequestID = %q, want %q", restored.RequestID, original.RequestID)
	}
	if restored.ClientVersion != original.ClientVersion {
		t.Errorf("ClientVersion = %q, want %q", restored.ClientVersion, original.ClientVersion)
	}
}

func TestResponse_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("success response", func(t *testing.T) {
		original := Response{
			Success: true,
			Data:    json.RawMessage(`{"id":"loom-123"}`),
		}

		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("json.Marshal() error: %v", err)
		}

		var restored Response
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatalf("json.Unmarshal() error: %v", err)
		}

		if !restored.Success {
			t.Error("Success should be true")
		}
		if restored.Error != "" {
			t.Errorf("Error should be empty, got %q", restored.Error)
		}
	})

	t.Run("error response", func(t *testing.T) {
		original := Response{
			Success: false,
			Error:   "something went wrong",
		}

		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("json.Marshal() error: %v", err)
		}

		var restored Response
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatalf("json.Unmarshal() error: %v", err)
		}

		if restored.Success {
			t.Error("Success should be false")
		}
		if restored.Error != "something went wrong" {
			t.Errorf("Error = %q, want %q", restored.Error, "something went wrong")
		}
	})
}

func TestCreateArgs_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	estimatedMinutes := 60
	original := CreateArgs{
		ID:                 "loom-test",
		Parent:             "loom-parent",
		Title:              "Test Issue",
		Description:        "Test description",
		IssueType:          "task",
		Priority:           2,
		Design:             "Implementation plan",
		AcceptanceCriteria: "All tests pass",
		Notes:              "Some notes",
		Assignee:           "alice",
		ExternalRef:        "gh-123",
		EstimatedMinutes:   &estimatedMinutes,
		Labels:             []string{"bug", "urgent"},
		Dependencies:       []string{"loom-1", "loom-2"},
		WaitsFor:           "loom-spawner",
		WaitsForGate:       "all-children",
		Sender:             "bot",
		Ephemeral:          true,
		RepliesTo:          "loom-original",
		IDPrefix:           "mol",
		CreatedBy:          "alice@example.com",
		Owner:              "bob@example.com",
		MolType:            "swarm",
		RoleType:           "polecat",
		Rig:                "rig-1",
		EventCategory:      "patrol.started",
		EventActor:         "entity://hop/gastown/org/agent",
		EventTarget:        "loom-target",
		EventPayload:       `{"key":"value"}`,
		DueAt:              "2024-12-31",
		DeferUntil:         "2024-01-01",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	var restored CreateArgs
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}

	if restored.Title != original.Title {
		t.Errorf("Title = %q, want %q", restored.Title, original.Title)
	}
	if restored.Priority != original.Priority {
		t.Errorf("Priority = %d, want %d", restored.Priority, original.Priority)
	}
	if *restored.EstimatedMinutes != estimatedMinutes {
		t.Errorf("EstimatedMinutes = %d, want %d", *restored.EstimatedMinutes, estimatedMinutes)
	}
	if len(restored.Labels) != 2 {
		t.Errorf("Labels length = %d, want 2", len(restored.Labels))
	}
	if restored.Ephemeral != original.Ephemeral {
		t.Errorf("Ephemeral = %v, want %v", restored.Ephemeral, original.Ephemeral)
	}
}

func TestCreateArgs_OmitEmpty(t *testing.T) {
	t.Parallel()

	// Minimal args - most fields should be omitted
	original := CreateArgs{
		Title:     "Test",
		IssueType: "task",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	jsonStr := string(data)
	// Should not contain omitempty fields when empty
	if strings.Contains(jsonStr, `"description"`) {
		t.Error("Empty description should be omitted")
	}
	if strings.Contains(jsonStr, `"notes"`) {
		t.Error("Empty notes should be omitted")
	}
	if strings.Contains(jsonStr, `"labels"`) {
		t.Error("Empty labels should be omitted")
	}
}

func TestUpdateArgs_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	title := "Updated Title"
	description := "Updated Description"
	status := "in_progress"
	priority := 1
	ephemeral := true

	original := UpdateArgs{
		ID:           "loom-123",
		Title:        &title,
		Description:  &description,
		Status:       &status,
		Priority:     &priority,
		AddLabels:    []string{"new-label"},
		RemoveLabels: []string{"old-label"},
		Ephemeral:    &ephemeral,
		Claim:        true,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	var restored UpdateArgs
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}

	if restored.ID != original.ID {
		t.Errorf("ID = %q, want %q", restored.ID, original.ID)
	}
	if *restored.Title != title {
		t.Errorf("Title = %q, want %q", *restored.Title, title)
	}
	if *restored.Priority != priority {
		t.Errorf("Priority = %d, want %d", *restored.Priority, priority)
	}
	if !restored.Claim {
		t.Error("Claim should be true")
	}
}

func TestUpdateArgs_PointerFields(t *testing.T) {
	t.Parallel()

	// Test nil vs set for pointer fields
	t.Run("nil pointer omitted", func(t *testing.T) {
		args := UpdateArgs{ID: "loom-123"}
		data, _ := json.Marshal(args)
		if strings.Contains(string(data), `"title"`) {
			t.Error("Nil title should be omitted")
		}
	})

	t.Run("set pointer included", func(t *testing.T) {
		title := "New Title"
		args := UpdateArgs{ID: "loom-123", Title: &title}
		data, _ := json.Marshal(args)
		if !strings.Contains(string(data), `"title"`) {
			t.Error("Set title should be included")
		}
	})
}

func TestCloseArgs_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := CloseArgs{
		ID:          "loom-123",
		Reason:      "completed successfully",
		Session:     "session-abc",
		SuggestNext: true,
		Force:       true,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	var restored CloseArgs
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}

	if restored.ID != original.ID {
		t.Errorf("ID = %q, want %q", restored.ID, original.ID)
	}
	if restored.Reason != original.Reason {
		t.Errorf("Reason = %q, want %q", restored.Reason, original.Reason)
	}
	if !restored.SuggestNext {
		t.Error("SuggestNext should be true")
	}
	if !restored.Force {
		t.Error("Force should be true")
	}
}

func TestListArgs_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	priority := 2
	pinned := true
	ephemeral := false

	original := ListArgs{
		Query:               "search term",
		Status:              "open",
		Priority:            &priority,
		IssueType:           "task",
		Assignee:            "alice",
		Labels:              []string{"bug", "urgent"},
		LabelsAny:           []string{"p0", "p1"},
		IDs:                 []string{"loom-1", "loom-2"},
		Limit:               50,
		TitleContains:       "test",
		DescriptionContains: "description",
		CreatedAfter:        "2024-01-01T00:00:00Z",
		CreatedBefore:       "2024-12-31T23:59:59Z",
		EmptyDescription:    true,
		NoAssignee:          true,
		Pinned:              &pinned,
		Ephemeral:           &ephemeral,
		ExcludeStatus:       []string{"closed", "tombstone"},
		Deferred:            true,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	var restored ListArgs
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}

	if restored.Query != original.Query {
		t.Errorf("Query = %q, want %q", restored.Query, original.Query)
	}
	if len(restored.Labels) != 2 {
		t.Errorf("Labels length = %d, want 2", len(restored.Labels))
	}
	if *restored.Pinned != pinned {
		t.Error("Pinned should be true")
	}
}

func TestReadyArgs_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := ReadyArgs{IncludeRecommended: true}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	if !strings.Contains(string(data), `"include_recommended":true`) {
		t.Fatalf("JSON = %s, want include_recommended=true", data)
	}

	var restored ReadyArgs
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if !restored.IncludeRecommended {
		t.Error("IncludeRecommended = false, want true")
	}

	empty, err := json.Marshal(ReadyArgs{})
	if err != nil {
		t.Fatalf("json.Marshal(empty) error: %v", err)
	}
	if strings.Contains(string(empty), "include_recommended") {
		t.Errorf("empty JSON = %s, want include_recommended omitted", empty)
	}
}

func TestDeleteArgs_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := DeleteArgs{
		IDs:     []string{"loom-1", "loom-2", "loom-3"},
		Force:   true,
		DryRun:  true,
		Cascade: true,
		Reason:  "cleanup",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	var restored DeleteArgs
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}

	if len(restored.IDs) != 3 {
		t.Errorf("IDs length = %d, want 3", len(restored.IDs))
	}
	if !restored.Force || !restored.DryRun || !restored.Cascade {
		t.Error("Boolean flags not preserved")
	}
}

func TestBatchArgs_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := BatchArgs{
		Operations: []BatchOperation{
			{Operation: OpCreate, Args: json.RawMessage(`{"title":"test1"}`)},
			{Operation: OpUpdate, Args: json.RawMessage(`{"id":"loom-1","status":"closed"}`)},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	var restored BatchArgs
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}

	if len(restored.Operations) != 2 {
		t.Errorf("Operations length = %d, want 2", len(restored.Operations))
	}
	if restored.Operations[0].Operation != OpCreate {
		t.Errorf("First operation = %q, want %q", restored.Operations[0].Operation, OpCreate)
	}
}

func TestOperationConstants(t *testing.T) {
	t.Parallel()

	expectedOps := map[string]string{
		"ping":                  OpPing,
		"status":                OpStatus,
		"health":                OpHealth,
		"metrics":               OpMetrics,
		"create":                OpCreate,
		"update":                OpUpdate,
		"close":                 OpClose,
		"list":                  OpList,
		"count":                 OpCount,
		"show":                  OpShow,
		"ready":                 OpReady,
		"blocked":               OpBlocked,
		"stale":                 OpStale,
		"stats":                 OpStats,
		"dep_add":               OpDepAdd,
		"dep_remove":            OpDepRemove,
		"dep_tree":              OpDepTree,
		"label_add":             OpLabelAdd,
		"label_remove":          OpLabelRemove,
		"comment_list":          OpCommentList,
		"comment_add":           OpCommentAdd,
		"event_list":            OpEventList,
		"batch":                 OpBatch,
		"resolve_id":            OpResolveID,
		"compact":               OpCompact,
		"compact_stats":         OpCompactStats,
		"export":                OpExport,
		"import":                OpImport,
		"epic_status":           OpEpicStatus,
		"get_mutations":         OpGetMutations,
		"get_molecule_progress": OpGetMoleculeProgress,
		"shutdown":              OpShutdown,
		"delete":                OpDelete,
		"get_worker_status":     OpGetWorkerStatus,
		"get_config":            OpGetConfig,
		"mol_stale":             OpMolStale,
		"get_parent_ids":        OpGetParentIDs,
		"get_graph_data":        OpGetGraphData,
		"wait_for_mutations":    OpWaitForMutations,
		"gate_create":           OpGateCreate,
		"gate_list":             OpGateList,
		"gate_show":             OpGateShow,
		"gate_close":            OpGateClose,
		"gate_wait":             OpGateWait,
	}

	for want, got := range expectedOps {
		if got != want {
			t.Errorf("Operation constant %q = %q, want %q", want, got, want)
		}
	}
}

func TestGateArgs_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("GateCreateArgs", func(t *testing.T) {
		original := GateCreateArgs{
			Title:     "Wait for PR",
			AwaitType: "gh:pr",
			AwaitID:   "123",
			Timeout:   5 * time.Minute,
			Waiters:   []string{"alice@example.com"},
		}

		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("json.Marshal() error: %v", err)
		}

		var restored GateCreateArgs
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatalf("json.Unmarshal() error: %v", err)
		}

		if restored.Title != original.Title {
			t.Errorf("Title = %q, want %q", restored.Title, original.Title)
		}
		if restored.AwaitType != original.AwaitType {
			t.Errorf("AwaitType = %q, want %q", restored.AwaitType, original.AwaitType)
		}
	})

	t.Run("GateListArgs", func(t *testing.T) {
		original := GateListArgs{All: true}
		data, _ := json.Marshal(original)
		var restored GateListArgs
		json.Unmarshal(data, &restored)
		if !restored.All {
			t.Error("All should be true")
		}
	})

	t.Run("GateShowArgs", func(t *testing.T) {
		original := GateShowArgs{ID: "gate-123"}
		data, _ := json.Marshal(original)
		var restored GateShowArgs
		json.Unmarshal(data, &restored)
		if restored.ID != "gate-123" {
			t.Errorf("ID = %q, want %q", restored.ID, "gate-123")
		}
	})

	t.Run("GateCloseArgs", func(t *testing.T) {
		original := GateCloseArgs{ID: "gate-123", Reason: "manual close"}
		data, _ := json.Marshal(original)
		var restored GateCloseArgs
		json.Unmarshal(data, &restored)
		if restored.ID != "gate-123" || restored.Reason != "manual close" {
			t.Errorf("GateCloseArgs not preserved correctly")
		}
	})

	t.Run("GateWaitArgs", func(t *testing.T) {
		original := GateWaitArgs{ID: "gate-123", Waiters: []string{"bob@example.com"}}
		data, _ := json.Marshal(original)
		var restored GateWaitArgs
		json.Unmarshal(data, &restored)
		if len(restored.Waiters) != 1 {
			t.Errorf("Waiters length = %d, want 1", len(restored.Waiters))
		}
	})
}
