package workitems

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStatsJSONRoundTrip(t *testing.T) {
	original := Stats{
		TotalIssues: 100, OpenIssues: 40, InProgressIssues: 15,
		ClosedIssues: 30, BlockedIssues: 5, DeferredIssues: 3,
		ReadyIssues: 7, PinnedIssues: 2, EpicsEligibleForClosure: 1,
		AverageLeadTime: 48.5,
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Stats
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != original {
		t.Fatalf("stats round trip = %+v, want %+v", decoded, original)
	}
}

func TestStatsJSONKeepsMeaningfulZeroValues(t *testing.T) {
	data, err := json.Marshal(Stats{TotalIssues: 10, OpenIssues: 5})
	if err != nil {
		t.Fatal(err)
	}
	raw := string(data)
	for _, field := range []string{`"average_lead_time_hours":0`, `"total_issues":10`, `"epics_eligible_for_closure":0`} {
		if !strings.Contains(raw, field) {
			t.Errorf("stats JSON %s does not contain %s", raw, field)
		}
	}
}

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
