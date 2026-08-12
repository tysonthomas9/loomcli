package handler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestDriverRunFromDomainMapsCanonicalGeneratedContract(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	run := &domain.DriverRun{
		WorkspaceKey: "WS", RunID: "run-1", DriverID: "driver", DriverVersionID: "v1",
		SubjectKey: "task:TASK-1", AwaitInstanceKey: "await-1", Status: domain.DriverRunRunning,
		Payload: json.RawMessage(`{"task_id":"TASK-1"}`), Output: map[string]string{"result": "ok"},
		CreatedAt: now, UpdatedAt: now,
	}
	got := DriverRunFromDomain(run)
	if got.RunId != run.RunID || got.SubjectKey == nil || *got.SubjectKey != run.SubjectKey ||
		got.AwaitInstanceKey == nil || *got.AwaitInstanceKey != run.AwaitInstanceKey {
		t.Fatalf("generated DriverRun = %+v", got)
	}
	(*got.Output)["result"] = "changed"
	if run.Output["result"] != "ok" {
		t.Fatal("generated output aliases the owner projection")
	}
}

func TestDriverRunsFromDomainReturnsEmptyArrayAndSkipsNil(t *testing.T) {
	if got := DriverRunsFromDomain(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil runs mapped to %#v, want non-nil empty array", got)
	}
	if got := DriverRunsFromDomain([]*domain.DriverRun{nil}); len(got) != 0 {
		t.Fatalf("nil run mapped to %#v, want skipped", got)
	}
}
