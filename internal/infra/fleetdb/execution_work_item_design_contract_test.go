package fleetdb

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

func TestExecutionTaskRunWorkItemDesignUsesOwnerFencedExactTaskRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/task-runs/task-run-1/work-item/design" {
			t.Fatalf("request = %s %s, want exact design command route", r.Method, r.URL.Path)
		}
		if token := r.Header.Get("X-Lease-Token"); token != "raw-task-secret" {
			t.Fatalf("X-Lease-Token = %q", token)
		}
		var raw map[string]json.RawMessage
		decodeJSONBody(t, r, &raw)
		for _, forbidden := range []string{"task_id", "issue_id", "lease_token", "updated_at"} {
			if _, present := raw[forbidden]; present {
				t.Fatalf("design command body contains forbidden field %q", forbidden)
			}
		}
		var body struct {
			CommandID    string  `json:"command_id"`
			NodeID       string  `json:"node_id"`
			LeaseID      string  `json:"lease_id"`
			FencingToken int64   `json:"fencing_token"`
			Design       string  `json:"design"`
			DesignFormat *string `json:"design_format"`
		}
		encoded, _ := json.Marshal(raw)
		if err := json.Unmarshal(encoded, &body); err != nil {
			t.Fatal(err)
		}
		if body.CommandID != "design-1" || body.NodeID != "node-1" || body.LeaseID != "lease-1" ||
			body.FencingToken != 7 || body.Design != "# Plan" || body.DesignFormat == nil || *body.DesignFormat != "markdown" {
			t.Fatalf("design command body = %+v", body)
		}
		writeJSON(t, w, validExecutionTaskRunWorkItemDesignResult())
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Execution().UpdateTaskRunWorkItemDesign(t.Context(), ExecutionTaskRunWorkItemDesignCommand{
		WorkspaceKey: "WS", CommandID: "design-1", TaskRunID: "task-run-1",
		NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "raw-task-secret", FencingToken: 7,
		Design: "# Plan",
	})
	if err != nil {
		t.Fatalf("UpdateTaskRunWorkItemDesign: %v", err)
	}
	if result.Committed.TaskID != "TASK-1" || result.Action.ActionID != "task-run-work-item-design-update:design-1" || !result.Replayed {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecutionTaskRunWorkItemDesignRejectsBlankBeforeTransport(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	command := taskRunWorkItemDesignCommand()
	command.Design = "  "
	if _, err := client.Execution().UpdateTaskRunWorkItemDesign(t.Context(), command); !errors.Is(err, ErrExecutionInvalid) {
		t.Fatalf("error = %v, want ErrExecutionInvalid", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("transport calls = %d, want 0", calls.Load())
	}
}

func TestExecutionTaskRunWorkItemDesignReplayAcceptsAdvancedCurrentProjection(t *testing.T) {
	fixture := validExecutionTaskRunWorkItemDesignResult()
	fixture.Replayed = true
	fixture.TaskRun.Status = execution.TaskRunRecordCompleted
	fixture.TaskRun.NodeID = "node-next"
	fixture.TaskRun.LeaseID = "lease-next"
	fixture.TaskRun.FencingToken++
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, fixture)
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Execution().UpdateTaskRunWorkItemDesign(t.Context(), taskRunWorkItemDesignCommand())
	if err != nil {
		t.Fatalf("replay with advanced TaskRun projection: %v", err)
	}
	if !result.Replayed || result.Committed.TaskID != "TASK-1" {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecutionTaskRunWorkItemDesignFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*ExecutionTaskRunWorkItemDesignResult)
	}{
		{"foreign issue", func(result *ExecutionTaskRunWorkItemDesignResult) { result.Issue.ID = "TASK-2" }},
		{"foreign committed task", func(result *ExecutionTaskRunWorkItemDesignResult) { result.Committed.TaskID = "TASK-2" }},
		{"wrong owner fence", func(result *ExecutionTaskRunWorkItemDesignResult) {
			result.Replayed = false
			result.TaskRun.FencingToken++
		}},
		{"wrong committed format", func(result *ExecutionTaskRunWorkItemDesignResult) { result.Committed.DesignFormat = "html" }},
		{"wrong committed digest", func(result *ExecutionTaskRunWorkItemDesignResult) { result.Committed.DesignSHA256 = "sha256:wrong" }},
		{"wrong action type", func(result *ExecutionTaskRunWorkItemDesignResult) { result.Action.ActionType = "update_issue" }},
		{"missing request fingerprint", func(result *ExecutionTaskRunWorkItemDesignResult) { result.Action.RequestRef = "" }},
		{"wrong response receipt", func(result *ExecutionTaskRunWorkItemDesignResult) { result.Action.ResponseRef += "-wrong" }},
		{"missing action time", func(result *ExecutionTaskRunWorkItemDesignResult) { result.Action.CreatedAt = time.Time{} }},
		{"mismatched commit time", func(result *ExecutionTaskRunWorkItemDesignResult) {
			result.Committed.UpdatedAt = result.Action.CreatedAt.Add(time.Second)
		}},
		{"mismatched applied time", func(result *ExecutionTaskRunWorkItemDesignResult) {
			changed := result.Action.CreatedAt.Add(time.Second)
			result.Action.AppliedAt = &changed
		}},
		{"non-replay terminal run", func(result *ExecutionTaskRunWorkItemDesignResult) {
			result.Replayed = false
			result.TaskRun.Status = execution.TaskRunRecordCompleted
		}},
		{"missing commit time", func(result *ExecutionTaskRunWorkItemDesignResult) { result.Committed.UpdatedAt = time.Time{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := validExecutionTaskRunWorkItemDesignResult()
			test.edit(&fixture)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(t, w, fixture)
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Execution().UpdateTaskRunWorkItemDesign(t.Context(), taskRunWorkItemDesignCommand())
			if !errors.Is(err, ErrExecutionUnavailable) {
				t.Fatalf("error = %v, want ErrExecutionUnavailable", err)
			}
		})
	}
}

func taskRunWorkItemDesignCommand() ExecutionTaskRunWorkItemDesignCommand {
	format := "markdown"
	return ExecutionTaskRunWorkItemDesignCommand{
		WorkspaceKey: "WS", CommandID: "design-1", TaskRunID: "task-run-1",
		NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "raw-task-secret", FencingToken: 7,
		Design: "# Plan", DesignFormat: &format,
	}
}

func validExecutionTaskRunWorkItemDesignResult() ExecutionTaskRunWorkItemDesignResult {
	updatedAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	appliedAt := updatedAt
	designDigest, designSHA256 := executionTaskRunWorkItemDesignDigest("# Plan")
	responseRef := executionTaskRunWorkItemDesignResponseRef("TASK-1", "markdown", designDigest, "artifact-design-1")
	return ExecutionTaskRunWorkItemDesignResult{
		TaskRun: &execution.TaskRunRecord{
			WorkspaceKey: "WS", TaskRunID: "task-run-1", TaskID: "TASK-1",
			Status: execution.TaskRunRecordRunning, NodeID: "node-1", LeaseID: "lease-1", FencingToken: 7,
		},
		Issue: &ExecutionIssue{
			ID: "TASK-1", Workspace: "WS", Title: "Plan this work", Status: "in_progress", UpdatedAt: updatedAt,
		},
		Action: &ExecutionActionLedger{
			WorkspaceKey: "WS", ActionID: "task-run-work-item-design-update:design-1",
			IdempotencyKey: "task-run-work-item-design-update:design-1", ActionType: "task_run_work_item_design_update",
			TargetRef: "task-run-1", RequestedBy: "node:node-1", Status: "applied",
			RequestRef: designSHA256, ResponseRef: responseRef, CreatedAt: updatedAt, AppliedAt: &appliedAt,
		},
		Committed: ExecutionTaskRunWorkItemDesignCommit{
			WorkspaceKey: "WS", TaskRunID: "task-run-1", TaskID: "TASK-1",
			DesignFormat: "markdown", DesignArtifactID: "artifact-design-1", DesignSHA256: designSHA256, UpdatedAt: updatedAt,
		},
		Replayed: true,
	}
}
