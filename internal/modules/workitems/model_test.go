package workitems

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMutationJSONRoundTripPreservesOpaqueCursor(t *testing.T) {
	original := Mutation{
		Cursor: "c1.MTcwMDAwMDAwMDUwMC0y", Type: MutationStatus,
		EntityType: "issue", EntityID: "TASK-1", IssueID: "TASK-1",
		Actor: "task-run:run-1", Timestamp: time.Date(2026, 8, 11, 19, 0, 0, 0, time.UTC),
		OldStatus: "in_progress", NewStatus: "review", SourceRepo: "loomcli",
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Mutation
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != original {
		t.Fatalf("mutation round trip = %+v, want %+v", decoded, original)
	}
}

func TestMutationTypeConstants(t *testing.T) {
	tests := map[string]string{
		"MutationCreate": MutationCreate, "MutationUpdate": MutationUpdate,
		"MutationDelete": MutationDelete, "MutationComment": MutationComment,
		"MutationBonded": MutationBonded, "MutationSquashed": MutationSquashed,
		"MutationBurned": MutationBurned, "MutationStatus": MutationStatus,
		"MutationRefresh": MutationRefresh, "MutationSessionChange": MutationSessionChange,
	}
	want := map[string]string{
		"MutationCreate": "create", "MutationUpdate": "update",
		"MutationDelete": "delete", "MutationComment": "comment",
		"MutationBonded": "bonded", "MutationSquashed": "squashed",
		"MutationBurned": "burned", "MutationStatus": "status",
		"MutationRefresh": "refresh", "MutationSessionChange": "session_change",
	}
	for name, got := range tests {
		if got != want[name] {
			t.Errorf("%s = %q, want %q", name, got, want[name])
		}
	}
}
