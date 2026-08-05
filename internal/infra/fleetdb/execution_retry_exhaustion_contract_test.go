package fleetdb

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestExecutionTaskRunRetryExhaustionAcceptsCertifiedBlockedAndPreservedOutcomes(t *testing.T) {
	for _, test := range []struct {
		name         string
		issueBlocked bool
		issue        *ExecutionIssue
		replayed     bool
		responseRef  string
	}{
		{
			name:         "fresh exact generation blocked",
			issueBlocked: true,
			issue:        retryExhaustionIssue("blocked"),
			responseRef:  "task-run://task-run-1#failed;linked-issue#blocked",
		},
		{
			name:         "fresh successor preserved",
			issueBlocked: false,
			issue:        retryExhaustionIssue("in_progress"),
			responseRef:  "task-run://task-run-1#failed;linked-issue#preserved",
		},
		{
			name:         "fresh defensively absent issue preserved",
			issueBlocked: false,
			issue:        nil,
			responseRef:  "task-run://task-run-1#failed;linked-issue#preserved",
		},
		{
			name:         "replayed block after successor advancement",
			issueBlocked: true,
			issue:        retryExhaustionIssue("in_progress"),
			replayed:     true,
			responseRef:  "task-run://task-run-1#failed;linked-issue#blocked",
		},
		{
			name:         "replayed block after issue removal",
			issueBlocked: true,
			issue:        nil,
			replayed:     true,
			responseRef:  "task-run://task-run-1#failed;linked-issue#blocked",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := validExecutionTaskRunRetryExhaustionResult(test.issueBlocked, test.issue, test.replayed)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/task-runs/task-run-1/exhaust-retries" {
					t.Fatalf("request = %s %s", r.Method, r.URL.Path)
				}
				if token := r.Header.Get("X-Lease-Token"); token != "raw-task-secret" {
					t.Fatalf("X-Lease-Token = %q", token)
				}
				writeJSON(t, w, fixture)
			}))
			t.Cleanup(server.Close)

			client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Execution().ExhaustTaskRunRetries(t.Context(), retryExhaustionCommand())
			if err != nil {
				t.Fatalf("ExhaustTaskRunRetries: %v", err)
			}
			if result.Committed.IssueBlocked != test.issueBlocked || result.Action.ResponseRef != test.responseRef ||
				result.Replayed != test.replayed || (result.Issue == nil) != (test.issue == nil) {
				t.Fatalf("result = %+v, want issue_blocked=%v response_ref=%q replayed=%v", result, test.issueBlocked, test.responseRef, test.replayed)
			}
		})
	}
}

func TestExecutionTaskRunRetryExhaustionRejectsForgedOutcomeAndDivergentIdentity(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*ExecutionTaskRunRetryExhaustionResult)
	}{
		{
			name: "forged preserved response for blocked receipt",
			edit: func(result *ExecutionTaskRunRetryExhaustionResult) {
				result.Action.ResponseRef = executionTaskRunRetryExhaustionResponseRef("task-run-1", false)
			},
		},
		{
			name: "forged noncanonical unchanged response",
			edit: func(result *ExecutionTaskRunRetryExhaustionResult) {
				result.Committed.IssueBlocked = false
				result.Action.ResponseRef = "task-run://task-run-1#failed;linked-issue#unchanged"
			},
		},
		{
			name: "fresh blocked receipt without blocked current issue",
			edit: func(result *ExecutionTaskRunRetryExhaustionResult) {
				result.Issue.Status = "in_progress"
			},
		},
		{
			name: "fresh blocked receipt without current issue",
			edit: func(result *ExecutionTaskRunRetryExhaustionResult) {
				result.Issue = nil
			},
		},
		{
			name: "committed work item differs from task run",
			edit: func(result *ExecutionTaskRunRetryExhaustionResult) {
				result.Committed.TaskID = "TASK-2"
			},
		},
		{
			name: "current issue differs from committed work item",
			edit: func(result *ExecutionTaskRunRetryExhaustionResult) {
				result.Issue.ID = "TASK-2"
			},
		},
		{
			name: "action targets another task run",
			edit: func(result *ExecutionTaskRunRetryExhaustionResult) {
				result.Action.ResponseRef = executionTaskRunRetryExhaustionResponseRef("task-run-2", true)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := validExecutionTaskRunRetryExhaustionResult(true, retryExhaustionIssue("blocked"), false)
			test.edit(&fixture)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(t, w, fixture)
			}))
			t.Cleanup(server.Close)

			client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Execution().ExhaustTaskRunRetries(t.Context(), retryExhaustionCommand())
			if !errors.Is(err, ErrExecutionUnavailable) {
				t.Fatalf("error = %v, want ErrExecutionUnavailable", err)
			}
		})
	}
}

func retryExhaustionCommand() ExecutionTaskRunRetryExhaustionCommand {
	return ExecutionTaskRunRetryExhaustionCommand{
		WorkspaceKey: "WS", CommandID: "exhaust-1", TaskRunID: "task-run-1",
		NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "raw-task-secret", FencingToken: 7,
		Attempt: 3, MaxAttempts: 3, ErrorClass: "agent_failed",
		ErrorMessage: "retry budget exhausted", FinishedAt: retryExhaustionFinishedAt(),
	}
}

func validExecutionTaskRunRetryExhaustionResult(issueBlocked bool, issue *ExecutionIssue, replayed bool) ExecutionTaskRunRetryExhaustionResult {
	finishedAt := retryExhaustionFinishedAt()
	at := finishedAt.Add(time.Second)
	appliedAt := at
	return ExecutionTaskRunRetryExhaustionResult{
		TaskRun: &domain.TaskRun{
			WorkspaceKey: "WS", TaskRunID: "task-run-1", TaskID: "TASK-1",
			Status: domain.TaskRunFailed, FinishedAt: &finishedAt,
		},
		Issue: issue,
		Action: &ExecutionActionLedger{
			WorkspaceKey: "WS", ActionID: "task-run-exhaust:exhaust-1",
			IdempotencyKey: "task-run-exhaust:exhaust-1", ActionType: "exhaust_task_run_retries",
			TargetRef: "task-run-1", RequestedBy: "node:node-1", Status: "applied",
			RequestRef:  "sha256:" + strings.Repeat("a", 64),
			ResponseRef: executionTaskRunRetryExhaustionResponseRef("task-run-1", issueBlocked),
			CreatedAt:   at, AppliedAt: &appliedAt,
		},
		Committed: ExecutionTaskRunRetryExhaustionCommit{
			WorkspaceKey: "WS", TaskRunID: "task-run-1", TaskID: "TASK-1",
			Status: domain.TaskRunFailed, IssueBlocked: issueBlocked, Attempt: 3, MaxAttempts: 3,
			ErrorClass: "agent_failed", ErrorMessage: "retry budget exhausted", FinishedAt: retryExhaustionFinishedAt(),
		},
		Replayed: replayed,
	}
}

func retryExhaustionIssue(status string) *ExecutionIssue {
	return &ExecutionIssue{
		ID: "TASK-1", Workspace: "WS", Title: "current work item", Status: status,
		Assignee: "driver-run:successor", UpdatedAt: retryExhaustionFinishedAt().Add(2 * time.Second),
	}
}

func retryExhaustionFinishedAt() time.Time {
	return time.Date(2026, 7, 17, 12, 0, 0, 123456000, time.UTC)
}
