package fleetdb

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestExecutionTerminalDriverStepRepairUsesSystemCommandRoute(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/driver-steps/step-1/repair-terminal" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if token := r.Header.Get("X-Lease-Token"); token != "" {
			t.Fatalf("terminal repair unexpectedly sent owner token %q", token)
		}
		var raw map[string]json.RawMessage
		decodeJSONBody(t, r, &raw)
		for _, forbidden := range []string{"node_id", "lease_id", "lease_token", "fencing_token", "repaired_at"} {
			if _, present := raw[forbidden]; present {
				t.Fatalf("terminal repair body contains forbidden owner/time field %q", forbidden)
			}
		}
		var body struct {
			CommandID   string                  `json:"command_id"`
			DriverRunID string                  `json:"driver_run_id"`
			TaskRunID   string                  `json:"task_run_id"`
			Status      domain.DriverStepStatus `json:"status"`
			OutputRef   string                  `json:"output_ref"`
		}
		encoded, _ := json.Marshal(raw)
		if err := json.Unmarshal(encoded, &body); err != nil {
			t.Fatal(err)
		}
		if body.CommandID != "repair-1" || body.DriverRunID != "run-1" || body.TaskRunID != "task-run-1" ||
			body.Status != domain.DriverStepCompleted || body.OutputRef != "artifact://result" {
			t.Fatalf("terminal repair body = %+v", body)
		}
		writeJSON(t, w, struct {
			DriverStep *domain.DriverStep `json:"driver_step"`
			Replayed   bool               `json:"replayed"`
		}{
			DriverStep: &domain.DriverStep{
				WorkspaceKey: "WS", StepID: "step-1", DriverRunID: "run-1", TaskRunID: "task-run-1",
				Status: domain.DriverStepCompleted, OutputRef: "artifact://result", CreatedAt: now, UpdatedAt: now,
			},
			Replayed: true,
		})
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	step, replayed, err := client.Execution().RepairTerminalDriverStep(t.Context(), store.TerminalDriverStepRepair{
		RequestID: "repair-1", WorkspaceKey: "WS", DriverRunID: "run-1", DriverStepID: "step-1",
		TaskRunID: "task-run-1", Status: domain.DriverStepCompleted, OutputRef: "artifact://result", RepairedAt: now,
	})
	if err != nil {
		t.Fatalf("RepairTerminalDriverStep: %v", err)
	}
	if !replayed || step == nil || step.StepID != "step-1" || step.Status != domain.DriverStepCompleted {
		t.Fatalf("RepairTerminalDriverStep() = %+v replayed=%v", step, replayed)
	}
}

func TestExecutionTaskRunTerminalConvergenceUsesTypedSystemRoutes(t *testing.T) {
	completedAt := time.Date(2026, 7, 18, 13, 0, 0, 123456000, time.UTC)
	requestNumber := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestNumber++
		if token := r.Header.Get("X-Lease-Token"); token != "" {
			t.Fatalf("terminal convergence unexpectedly sent owner token %q", token)
		}
		switch requestNumber {
		case 1:
			if r.Method != http.MethodGet || r.URL.Path != "/api/v1/WS/task-runs/terminal-convergence-candidates" {
				t.Fatalf("candidate request = %s %s", r.Method, r.URL.Path)
			}
			if r.URL.Query().Get("required_version") != "2" || r.URL.Query().Get("after") != "task-a" || r.URL.Query().Get("limit") != "7" {
				t.Fatalf("candidate query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, store.TaskRunTerminalConvergencePage{TaskRunIDs: []string{"task-b", "task-c"}, Next: "task-c"})
		case 2:
			if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/task-runs/task-b/complete-terminal-convergence" {
				t.Fatalf("completion request = %s %s", r.Method, r.URL.Path)
			}
			var body struct {
				RequiredVersion int       `json:"required_version"`
				CompletedAt     time.Time `json:"completed_at"`
			}
			decodeJSONBody(t, r, &body)
			if body.RequiredVersion != 2 || !body.CompletedAt.Equal(completedAt) {
				t.Fatalf("completion body = %+v", body)
			}
			writeJSON(t, w, store.TaskRunTerminalConvergenceResult{
				TaskRun: &domain.TaskRun{
					WorkspaceKey: "WS", TaskRunID: "task-b", TaskID: "TASK-1", Status: domain.TaskRunCompleted,
					TerminalConvergenceVersion: 2, TerminalConvergedAt: &completedAt,
				},
				Replayed: true,
			})
		default:
			t.Fatalf("unexpected request %d: %s %s", requestNumber, r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.Execution().ListTaskRunTerminalConvergenceCandidates(t.Context(), store.TaskRunTerminalConvergenceQuery{
		WorkspaceKey: "WS", RequiredVersion: 2, After: "task-a", Limit: 7,
	})
	if err != nil || len(page.TaskRunIDs) != 2 || page.Next != "task-c" {
		t.Fatalf("candidate page = %+v, %v", page, err)
	}
	result, err := client.Execution().CompleteTaskRunTerminalConvergence(t.Context(), store.TaskRunTerminalConvergenceComplete{
		WorkspaceKey: "WS", TaskRunID: "task-b", RequiredVersion: 2, CompletedAt: completedAt,
	})
	if err != nil || result == nil || !result.Replayed || result.TaskRun.TerminalConvergenceVersion != 2 {
		t.Fatalf("completion result = %+v, %v", result, err)
	}
}

func TestExecutionTaskRunTerminalConvergenceRejectsDivergentEnvelopes(t *testing.T) {
	completedAt := time.Now().UTC()
	for _, test := range []struct {
		name string
		body any
		list bool
	}{
		{name: "unordered page", list: true, body: store.TaskRunTerminalConvergencePage{TaskRunIDs: []string{"task-c", "task-b"}}},
		{name: "divergent marker", body: store.TaskRunTerminalConvergenceResult{TaskRun: &domain.TaskRun{
			WorkspaceKey: "OTHER", TaskRunID: "task-b", Status: domain.TaskRunCompleted,
			TerminalConvergenceVersion: 2, TerminalConvergedAt: &completedAt,
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeJSON(t, w, test.body) }))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			if test.list {
				_, err = client.Execution().ListTaskRunTerminalConvergenceCandidates(t.Context(), store.TaskRunTerminalConvergenceQuery{
					WorkspaceKey: "WS", RequiredVersion: 2, After: "task-a", Limit: 7,
				})
			} else {
				_, err = client.Execution().CompleteTaskRunTerminalConvergence(t.Context(), store.TaskRunTerminalConvergenceComplete{
					WorkspaceKey: "WS", TaskRunID: "task-b", RequiredVersion: 2, CompletedAt: completedAt,
				})
			}
			if !errors.Is(err, ErrExecutionUnavailable) {
				t.Fatalf("error = %v, want ErrExecutionUnavailable", err)
			}
		})
	}
}

func TestExecutionTaskRunClaimUsesSpecificOrClaimNextRouteAndReturnsLinkedStep(t *testing.T) {
	for _, tc := range []struct {
		name       string
		taskRunID  string
		wantPath   string
		returnedID string
	}{
		{name: "specific", taskRunID: "task-run-1", wantPath: "/api/v1/WS/task-runs/task-run-1/claim-and-start", returnedID: "task-run-1"},
		{name: "next", wantPath: "/api/v1/WS/task-runs/claim-next-and-start", returnedID: "task-run-next"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != tc.wantPath {
					t.Fatalf("request = %s %s, want POST %s", r.Method, r.URL.Path, tc.wantPath)
				}
				if got := r.Header.Get("X-Lease-Token"); got != "raw-task-secret" {
					t.Fatalf("X-Lease-Token = %q", got)
				}
				var body struct {
					CommandID string `json:"command_id"`
					NodeID    string `json:"node_id"`
					LeaseID   string `json:"lease_id"`
				}
				decodeJSONBody(t, r, &body)
				if body.CommandID != "claim-1" || body.NodeID != "node-1" || body.LeaseID != "lease-1" {
					t.Fatalf("claim body = %+v", body)
				}
				writeJSON(t, w, validExecutionClaimAndStartResult(tc.returnedID))
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Execution().ClaimAndStartTaskRun(t.Context(), ExecutionClaimAndStartCommand{
				WorkspaceKey: "WS", CommandID: "claim-1", TaskRunID: tc.taskRunID,
				NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "raw-task-secret", ClaimTTL: time.Minute,
			})
			if err != nil {
				t.Fatalf("ClaimAndStartTaskRun: %v", err)
			}
			if result.TaskRun.TaskRunID != tc.returnedID || result.DriverStep == nil ||
				result.DriverStep.TaskRunID != tc.returnedID || !result.Replayed {
				t.Fatalf("claim result = %+v", result)
			}
		})
	}
}

func TestExecutionTaskRunClaimNextEmptyMapsToNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/WS/task-runs/claim-next-and-start" {
			t.Fatalf("request path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Execution().ClaimAndStartTaskRun(t.Context(), ExecutionClaimAndStartCommand{
		WorkspaceKey: "WS", CommandID: "claim-empty-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "raw-task-secret", ClaimTTL: time.Minute,
	})
	if !errors.Is(err, ErrExecutionNotFound) {
		t.Fatalf("empty claim-next error = %v, want ErrExecutionNotFound", err)
	}
}

func TestExecutionTaskRunClaimNextMalformedSuccessFailsClosed(t *testing.T) {
	for _, body := range []string{"", `{}`} {
		t.Run("body_"+strings.ReplaceAll(body, "{}", "object"), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Execution().ClaimAndStartTaskRun(t.Context(), ExecutionClaimAndStartCommand{
				WorkspaceKey: "WS", CommandID: "claim-empty-200", NodeID: "node-1",
				LeaseID: "lease-1", LeaseToken: "raw-task-secret", ClaimTTL: time.Minute,
			})
			if !errors.Is(err, ErrExecutionUnavailable) {
				t.Fatalf("malformed HTTP 200 error = %v, want ErrExecutionUnavailable", err)
			}
		})
	}
}

func TestExecutionTaskRunClaimRejectsUnexpectedSuccessStatus(t *testing.T) {
	for _, tc := range []struct {
		name       string
		taskRunID  string
		returnedID string
		status     int
		writeBody  bool
	}{
		{name: "claim_next_created", returnedID: "task-run-next", status: http.StatusCreated, writeBody: true},
		{name: "claim_next_accepted", returnedID: "task-run-next", status: http.StatusAccepted, writeBody: true},
		{name: "specific_created", taskRunID: "task-run-1", returnedID: "task-run-1", status: http.StatusCreated, writeBody: true},
		{name: "specific_accepted", taskRunID: "task-run-1", returnedID: "task-run-1", status: http.StatusAccepted, writeBody: true},
		{name: "specific_no_content", taskRunID: "task-run-1", status: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				if tc.writeBody {
					if err := json.NewEncoder(w).Encode(validExecutionClaimAndStartResult(tc.returnedID)); err != nil {
						t.Fatalf("encode response: %v", err)
					}
				}
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Execution().ClaimAndStartTaskRun(t.Context(), ExecutionClaimAndStartCommand{
				WorkspaceKey: "WS", CommandID: "claim-1", TaskRunID: tc.taskRunID,
				NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "raw-task-secret", ClaimTTL: time.Minute,
			})
			if !errors.Is(err, ErrExecutionUnavailable) {
				t.Fatalf("HTTP %d claim error = %v, want ErrExecutionUnavailable", tc.status, err)
			}
		})
	}
}

func TestExecutionTaskRunClaimRejectsDivergentIssueAndActionReceipt(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*ExecutionClaimAndStartResult)
	}{
		{name: "issue_status", edit: func(result *ExecutionClaimAndStartResult) { result.Issue.Status = "open" }},
		{name: "issue_assignee", edit: func(result *ExecutionClaimAndStartResult) { result.Issue.Assignee = "driver-run:other" }},
		{name: "action_status", edit: func(result *ExecutionClaimAndStartResult) { result.Action.Status = "pending" }},
		{name: "action_requester", edit: func(result *ExecutionClaimAndStartResult) { result.Action.RequestedBy = "node:other" }},
		{name: "action_request_ref", edit: func(result *ExecutionClaimAndStartResult) { result.Action.RequestRef = "" }},
		{name: "action_response_ref", edit: func(result *ExecutionClaimAndStartResult) { result.Action.ResponseRef = "task-run://other#running" }},
		{name: "action_created_at", edit: func(result *ExecutionClaimAndStartResult) { result.Action.CreatedAt = time.Time{} }},
		{name: "action_applied_at", edit: func(result *ExecutionClaimAndStartResult) { result.Action.AppliedAt = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := validExecutionClaimAndStartResult("task-run-next")
			tc.edit(&result)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(t, w, result)
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Execution().ClaimAndStartTaskRun(t.Context(), ExecutionClaimAndStartCommand{
				WorkspaceKey: "WS", CommandID: "claim-1", NodeID: "node-1",
				LeaseID: "lease-1", LeaseToken: "raw-task-secret", ClaimTTL: time.Minute,
			})
			if !errors.Is(err, ErrExecutionUnavailable) {
				t.Fatalf("divergent receipt error = %v, want ErrExecutionUnavailable", err)
			}
		})
	}
}

func validExecutionClaimAndStartResult(taskRunID string) ExecutionClaimAndStartResult {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	appliedAt := now
	return ExecutionClaimAndStartResult{
		TaskRun: &domain.TaskRun{
			WorkspaceKey: "WS", TaskRunID: taskRunID, DriverRunID: "run-1", DriverStepID: "step-1",
			TaskID: "TASK-1", Status: domain.TaskRunRunning, NodeID: "node-1", LeaseID: "lease-1", FencingToken: 7,
		},
		DriverStep: &domain.DriverStep{
			WorkspaceKey: "WS", StepID: "step-1", DriverRunID: "run-1",
			TaskRunID: taskRunID, Status: domain.DriverStepRunning, ActionLedgerID: "task-run-start:claim-1",
		},
		Issue: &ExecutionIssue{ID: "TASK-1", Status: "in_progress", Assignee: "driver-run:run-1", UpdatedAt: now},
		Action: &ExecutionActionLedger{
			WorkspaceKey: "WS", ActionID: "task-run-start:claim-1", ActionType: "start_task_run",
			IdempotencyKey: "task-run-start:claim-1", TargetRef: taskRunID, RequestedBy: "node:node-1",
			Status: "applied", RequestRef: "sha256:test-fingerprint", ResponseRef: "task-run://" + taskRunID + "#running",
			CreatedAt: now, AppliedAt: &appliedAt,
		},
		Replayed: true,
	}
}

func TestExecutionTaskRunRequestCarriesExactClaimActionID(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 123456789, time.UTC)
	persistedAt := now.Truncate(time.Microsecond)
	claimActionID := "driver-run-work-item-claim:claim-work-item:sha256:" + strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/driver-runs/run-1/task-runs/request" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Lease-Token"); got != "raw-driver-secret" {
			t.Fatalf("X-Lease-Token = %q", got)
		}
		var raw map[string]json.RawMessage
		decodeJSONBody(t, r, &raw)
		if string(raw["claim_action_id"]) != `"`+claimActionID+`"` {
			t.Fatalf("claim_action_id = %s", raw["claim_action_id"])
		}
		if strings.Contains(string(raw["claim_action_id"]), "raw-driver-secret") {
			t.Fatal("request body leaked raw token")
		}
		writeJSON(t, w, executionTaskRunRequestFixture(persistedAt, claimActionID))
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Execution().RequestTaskRun(t.Context(), ExecutionTaskRunRequestCommand{
		WorkspaceKey: "WS", CommandID: "request-1", TaskRunID: "task-run-1", DriverRunID: "run-1",
		DriverStepID: "step-1", TaskID: "TASK-1", ClaimActionID: claimActionID,
		NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "raw-driver-secret", FencingToken: 7,
		RequestedAt: now,
	})
	if err != nil || result == nil || result.TaskRun == nil || result.ClaimActionID != claimActionID {
		t.Fatalf("RequestTaskRun() = %+v, %v", result, err)
	}
}

func executionTaskRunRequestFixture(at time.Time, claimActionID string) ExecutionTaskRunRequestResult {
	appliedAt := at
	return ExecutionTaskRunRequestResult{
		TaskRun: &domain.TaskRun{
			WorkspaceKey: "WS", TaskRunID: "task-run-1", DriverRunID: "run-1",
			DriverStepID: "step-1", TaskID: "TASK-1", Status: domain.TaskRunQueued,
		},
		DriverStep: &domain.DriverStep{
			WorkspaceKey: "WS", StepID: "step-1", DriverRunID: "run-1",
			TaskRunID: "task-run-1", Status: domain.DriverStepQueued, ActionLedgerID: "task-run-request:request-1",
		},
		Action: &ExecutionActionLedger{
			WorkspaceKey: "WS", ActionID: "task-run-request:request-1",
			IdempotencyKey: "task-run-request:request-1", ActionType: "request_task_run", TargetRef: "task-run-1",
			RequestedBy: "driver-run:run-1", Status: "applied", RequestRef: "sha256:" + strings.Repeat("b", 64),
			ResponseRef: "task-run://task-run-1#queued", CreatedAt: at, AppliedAt: &appliedAt,
		},
		ClaimActionID:             claimActionID,
		CommittedTaskRunStatus:    domain.TaskRunQueued,
		CommittedDriverStepStatus: domain.DriverStepQueued,
	}
}

func TestExecutionTaskRunRequestRejectsDivergentClaimAndActionReceipts(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 123456789, time.UTC)
	claimActionID := "driver-run-work-item-claim:claim-work-item:sha256:" + strings.Repeat("a", 64)
	for _, test := range []struct {
		name string
		edit func(*ExecutionTaskRunRequestResult)
	}{
		{name: "missing claim action", edit: func(result *ExecutionTaskRunRequestResult) { result.ClaimActionID = "" }},
		{name: "wrong claim action", edit: func(result *ExecutionTaskRunRequestResult) { result.ClaimActionID = "driver-run-work-item-claim:other" }},
		{name: "wrong requested by", edit: func(result *ExecutionTaskRunRequestResult) { result.Action.RequestedBy = "driver-run:other" }},
		{name: "invalid request fingerprint", edit: func(result *ExecutionTaskRunRequestResult) { result.Action.RequestRef = "not-a-digest" }},
		{name: "wrong applied timestamp", edit: func(result *ExecutionTaskRunRequestResult) {
			result.Action.CreatedAt = result.Action.CreatedAt.Add(time.Microsecond)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				result := executionTaskRunRequestFixture(now.Truncate(time.Microsecond), claimActionID)
				test.edit(&result)
				writeJSON(t, w, result)
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Execution().RequestTaskRun(t.Context(), ExecutionTaskRunRequestCommand{
				WorkspaceKey: "WS", CommandID: "request-1", TaskRunID: "task-run-1", DriverRunID: "run-1",
				DriverStepID: "step-1", TaskID: "TASK-1", ClaimActionID: claimActionID,
				NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "raw-driver-secret", FencingToken: 7,
				RequestedAt: now,
			})
			if !errors.Is(err, ErrExecutionUnavailable) {
				t.Fatalf("RequestTaskRun() error = %v, want unavailable", err)
			}
		})
	}
}

func TestExecutionDriverRunWorkItemCommandsUseOwnerFencedRoutesAndStrictReceipts(t *testing.T) {
	claimedAt := time.Date(2026, 7, 17, 12, 0, 0, 123456789, time.UTC)
	releasedAt := claimedAt.Add(time.Minute)
	claimCommandID := "claim-work-item:sha256:" + strings.Repeat("a", 64)
	releaseCommandID := "release-work-item:sha256:" + strings.Repeat("b", 64)
	claimActionID := "driver-run-work-item-claim:" + claimCommandID
	requestNumber := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestNumber++
		wantPath := "/api/v1/WS/driver-runs/run-1/work-items/TASK-1/claim"
		if requestNumber == 2 {
			wantPath = "/api/v1/WS/driver-runs/run-1/work-items/TASK-1/release"
		}
		if r.Method != http.MethodPost || r.URL.Path != wantPath {
			t.Fatalf("request = %s %s, want POST %s", r.Method, r.URL.Path, wantPath)
		}
		if got := r.Header.Get("X-Lease-Token"); got != "raw-driver-secret" {
			t.Fatalf("X-Lease-Token = %q", got)
		}
		var raw map[string]json.RawMessage
		decodeJSONBody(t, r, &raw)
		encoded, _ := json.Marshal(raw)
		if strings.Contains(string(encoded), "raw-driver-secret") || raw["lease_token"] != nil {
			t.Fatalf("body leaked raw token: %s", encoded)
		}
		if string(raw["node_id"]) != `"node-1"` || string(raw["lease_id"]) != `"lease-1"` || string(raw["fencing_token"]) != "7" {
			t.Fatalf("owner body = %s", encoded)
		}
		if requestNumber == 1 {
			if string(raw["command_id"]) != `"`+claimCommandID+`"` || string(raw["claim_ttl_seconds"]) != "300" {
				t.Fatalf("claim body = %s", encoded)
			}
			writeJSON(t, w, executionDriverRunWorkItemFixture("claim", claimCommandID, claimedAt.Truncate(time.Microsecond)))
			return
		}
		if string(raw["command_id"]) != `"`+releaseCommandID+`"` || string(raw["claim_action_id"]) != `"`+claimActionID+`"` {
			t.Fatalf("release body = %s", encoded)
		}
		writeJSON(t, w, executionDriverRunWorkItemFixture("release", releaseCommandID, releasedAt.Truncate(time.Microsecond)))
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := client.Execution().ClaimDriverRunWorkItem(t.Context(), ExecutionDriverRunWorkItemClaimCommand{
		WorkspaceKey: "WS", CommandID: claimCommandID, RunID: "run-1", TaskID: "TASK-1",
		NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "raw-driver-secret", FencingToken: 7,
		ClaimTTL: 5 * time.Minute, ClaimedAt: claimedAt,
	})
	if err != nil || claim.Action.ActionID != claimActionID {
		t.Fatalf("ClaimDriverRunWorkItem() = %+v, %v", claim, err)
	}
	release, err := client.Execution().ReleaseDriverRunWorkItem(t.Context(), ExecutionDriverRunWorkItemReleaseCommand{
		WorkspaceKey: "WS", CommandID: releaseCommandID, RunID: "run-1", TaskID: "TASK-1",
		NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "raw-driver-secret", FencingToken: 7,
		ClaimActionID: claimActionID, ReleasedAt: releasedAt,
	})
	if err != nil || release.Issue.Status != "open" {
		t.Fatalf("ReleaseDriverRunWorkItem() = %+v, %v", release, err)
	}
}

func TestExecutionDriverRunWorkItemCommandsRejectUnexpectedStatusAndDivergentReceipt(t *testing.T) {
	at := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	commandID := "claim-work-item:sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct {
		name   string
		status int
		edit   func(*ExecutionDriverRunWorkItemResult)
	}{
		{name: "created_status", status: http.StatusCreated},
		{name: "wrong_actor", status: http.StatusOK, edit: func(result *ExecutionDriverRunWorkItemResult) { result.Issue.Assignee = "driver-run:other" }},
		{name: "wrong_action", status: http.StatusOK, edit: func(result *ExecutionDriverRunWorkItemResult) { result.Action.ActionID = "other" }},
		{name: "wrong_response_ref", status: http.StatusOK, edit: func(result *ExecutionDriverRunWorkItemResult) { result.Action.ResponseRef = "issue://OTHER#claimed" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				result := executionDriverRunWorkItemFixture("claim", commandID, at)
				if tc.edit != nil {
					tc.edit(&result)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(result)
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Execution().ClaimDriverRunWorkItem(t.Context(), ExecutionDriverRunWorkItemClaimCommand{
				WorkspaceKey: "WS", CommandID: commandID, RunID: "run-1", TaskID: "TASK-1",
				NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "raw-driver-secret", FencingToken: 7, ClaimedAt: at,
			})
			if !errors.Is(err, ErrExecutionUnavailable) {
				t.Fatalf("error = %v, want ErrExecutionUnavailable", err)
			}
		})
	}
}

func TestExecutionDriverRunReviewClaimAcceptsOnlyVersionedReceiptFingerprint(t *testing.T) {
	at := time.Date(2026, 7, 23, 12, 44, 45, 841059362, time.UTC)
	commandID := "claim-work-item:sha256:" + strings.Repeat("a", 64)
	versionedRef := executionDriverRunReviewClaimFingerprintPrefix + "sha256:" + strings.Repeat("c", 64)
	for _, tc := range []struct {
		name           string
		requiredStatus string
		requestRef     string
		wantErr        bool
	}{
		{name: "review_versioned", requiredStatus: "review", requestRef: versionedRef},
		{name: "review_legacy_rejected", requiredStatus: "review", requestRef: "sha256:" + strings.Repeat("c", 64), wantErr: true},
		{name: "open_versioned_rejected", requestRef: versionedRef, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var raw map[string]json.RawMessage
				decodeJSONBody(t, r, &raw)
				if got := strings.Trim(string(raw["required_status"]), `"`); got != tc.requiredStatus {
					t.Fatalf("required_status = %q, want %q", got, tc.requiredStatus)
				}
				result := executionDriverRunWorkItemFixture("claim", commandID, at.Truncate(time.Microsecond))
				result.Action.RequestRef = tc.requestRef
				writeJSON(t, w, result)
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Execution().ClaimDriverRunWorkItem(t.Context(), ExecutionDriverRunWorkItemClaimCommand{
				WorkspaceKey: "WS", CommandID: commandID, RunID: "run-1", TaskID: "TASK-1",
				NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "raw-driver-secret", FencingToken: 7,
				RequiredStatus: tc.requiredStatus, ClaimedAt: at,
			})
			if tc.wantErr {
				if !errors.Is(err, ErrExecutionUnavailable) {
					t.Fatalf("error = %v, want ErrExecutionUnavailable", err)
				}
				return
			}
			if err != nil || result == nil || result.Action == nil {
				t.Fatalf("ClaimDriverRunWorkItem() = %+v, %v", result, err)
			}
		})
	}
}

func TestExecutionDriverRunReviewHandoffUsesFencedRouteAndStrictReceipt(t *testing.T) {
	handedOffAt := time.Date(2026, 7, 23, 5, 30, 0, 123456789, time.UTC)
	commandID := "handoff-review-work-item:sha256:" + strings.Repeat("d", 64)
	claimActionID := "driver-run-work-item-claim:claim-work-item:sha256:" + strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost ||
			r.URL.Path != "/api/v1/WS/driver-runs/run-1/work-items/TASK-1/review-handoff" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Lease-Token"); got != "raw-driver-secret" {
			t.Fatalf("X-Lease-Token = %q", got)
		}
		var raw map[string]json.RawMessage
		decodeJSONBody(t, r, &raw)
		encoded, _ := json.Marshal(raw)
		if strings.Contains(string(encoded), "raw-driver-secret") || raw["lease_token"] != nil {
			t.Fatalf("body leaked raw token: %s", encoded)
		}
		if string(raw["command_id"]) != `"`+commandID+`"` ||
			string(raw["claim_action_id"]) != `"`+claimActionID+`"` ||
			string(raw["task_run_id"]) != `"review-child-1"` ||
			string(raw["target_status"]) != `"closed"` ||
			string(raw["reason"]) != `"approved"` {
			t.Fatalf("handoff body = %s", encoded)
		}
		writeJSON(t, w, executionDriverRunWorkItemFixture("handoff", commandID, handedOffAt.Truncate(time.Microsecond)))
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Execution().HandoffDriverRunReviewWorkItem(t.Context(), ExecutionDriverRunReviewWorkItemHandoffCommand{
		WorkspaceKey: "WS", CommandID: commandID, RunID: "run-1", TaskID: "TASK-1",
		NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "raw-driver-secret", FencingToken: 7,
		ClaimActionID: claimActionID, TaskRunID: "review-child-1", TargetStatus: "closed",
		Reason: "approved", HandedOffAt: handedOffAt,
	})
	if err != nil || result.Issue == nil || result.Issue.Status != "closed" || result.Issue.Assignee != "" {
		t.Fatalf("HandoffDriverRunReviewWorkItem() = %+v, %v", result, err)
	}
}

func executionDriverRunWorkItemFixture(operation, commandID string, at time.Time) ExecutionDriverRunWorkItemResult {
	appliedAt := at
	status, assignee, actionType, responseState := "in_progress", "driver-run:run-1", "claim_work_item", "claimed"
	actionPrefix := "driver-run-work-item-claim:"
	if operation == "release" {
		status, assignee, actionType, responseState = "open", "", "release_work_item", "released"
		actionPrefix = "driver-run-work-item-release:"
	} else if operation == "handoff" {
		status, assignee, actionType, responseState = "closed", "", "handoff_review_work_item", "handed-off"
		actionPrefix = "driver-run-review-work-item-handoff:"
	}
	actionID := actionPrefix + commandID
	return ExecutionDriverRunWorkItemResult{
		Issue: &ExecutionIssue{ID: "TASK-1", Workspace: "WS", Title: "task", Status: status, Assignee: assignee, UpdatedAt: at},
		Action: &ExecutionActionLedger{
			WorkspaceKey: "WS", ActionID: actionID, IdempotencyKey: actionID, ActionType: actionType,
			TargetRef: "TASK-1", RequestedBy: "driver-run:run-1", Status: "applied",
			RequestRef: "sha256:" + strings.Repeat("c", 64), ResponseRef: "issue://TASK-1#" + responseState,
			CreatedAt: at, AppliedAt: &appliedAt,
		},
	}
}

func TestExecutionDriverRunSuspendUsesWriteOnlyLeaseTokenHeader(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/driver-runs/run-1/suspend" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Lease-Token"); got != "raw-driver-secret" {
			t.Fatalf("X-Lease-Token = %q", got)
		}
		var raw map[string]json.RawMessage
		decodeJSONBody(t, r, &raw)
		if _, leaked := raw["lease_token"]; leaked {
			t.Fatal("suspend body leaked lease_token")
		}
		var body struct {
			NodeID           string `json:"node_id"`
			LeaseID          string `json:"lease_id"`
			FencingToken     int64  `json:"fencing_token"`
			AwaitInstanceKey string `json:"await_instance_key"`
		}
		encoded, _ := json.Marshal(raw)
		if err := json.Unmarshal(encoded, &body); err != nil {
			t.Fatal(err)
		}
		if body.NodeID != "node-1" || body.LeaseID != "lease-1" || body.FencingToken != 7 || body.AwaitInstanceKey != "run-1#await-1" {
			t.Fatalf("suspend body = %+v", body)
		}
		writeJSON(t, w, domain.DriverRun{
			WorkspaceKey: "WS", RunID: "run-1", Status: domain.DriverRunSuspendedAwaitingEvent,
			AwaitInstanceKey: "run-1#await-1", CreatedAt: now, UpdatedAt: now,
		})
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := client.Execution().SuspendDriverRun(t.Context(), ExecutionDriverRunSuspendCommand{
		WorkspaceKey: "WS", RunID: "run-1", NodeID: "node-1", LeaseID: "lease-1",
		LeaseToken: "raw-driver-secret", FencingToken: 7, AwaitInstanceKey: "run-1#await-1",
	})
	if err != nil || run.Status != domain.DriverRunSuspendedAwaitingEvent {
		t.Fatalf("SuspendDriverRun() = %#v, %v", run, err)
	}
}

func TestExecutionStaleChildRecoveryUsesParentOwnerHeaderAndNoSecretBody(t *testing.T) {
	staleBefore := time.Date(2026, 7, 16, 11, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/driver-runs/run-1/recover-stale-tasks" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Lease-Token"); got != "raw-driver-secret" {
			t.Fatalf("X-Lease-Token = %q", got)
		}
		var raw map[string]json.RawMessage
		decodeJSONBody(t, r, &raw)
		body, _ := json.Marshal(raw)
		if strings.Contains(string(body), "raw-driver-secret") {
			t.Fatalf("recovery body leaked raw token: %s", body)
		}
		var request struct {
			RequestID    string    `json:"request_id"`
			NodeID       string    `json:"node_id"`
			LeaseID      string    `json:"lease_id"`
			FencingToken int64     `json:"fencing_token"`
			StaleBefore  time.Time `json:"stale_before"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		if request.RequestID != "recover-1" || request.NodeID != "node-1" || request.LeaseID != "lease-1" ||
			request.FencingToken != 7 || !request.StaleBefore.Equal(staleBefore) {
			t.Fatalf("recovery request = %+v", request)
		}
		writeJSON(t, w, ExecutionDriverRunStaleTaskRecoveryResult{
			WorkspaceKey: "WS", DriverRunID: "run-1", StaleBefore: staleBefore,
			RecoveredAt: staleBefore.Add(time.Minute), Recovered: 1, Released: 1,
			RecoveredTaskRunIDs: []string{"task-run-1"},
		})
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Execution().RecoverStaleChildTaskRuns(t.Context(), ExecutionDriverRunStaleTaskRecoveryCommand{
		WorkspaceKey: "WS", RequestID: "recover-1", RunID: "run-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "raw-driver-secret", FencingToken: 7,
		StaleBefore: staleBefore, ErrorClass: "stale_task_run", ErrorMessage: "heartbeat stale",
	})
	if err != nil || result.Recovered != 1 {
		t.Fatalf("RecoverStaleChildTaskRuns() = %#v, %v", result, err)
	}
}

func TestExecutionDriverRunCommandsRejectMissingRawTokenBeforeWire(t *testing.T) {
	client, err := New(Config{BaseURL: "http://unused.invalid", Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Execution().SuspendDriverRun(t.Context(), ExecutionDriverRunSuspendCommand{
		WorkspaceKey: "WS", RunID: "run-1", NodeID: "node-1", LeaseID: "lease-1",
		FencingToken: 7, AwaitInstanceKey: "run-1#await-1",
	}); !strings.Contains(err.Error(), "token") {
		t.Fatalf("SuspendDriverRun missing token error = %v", err)
	}
	if _, err := client.Execution().RecoverStaleChildTaskRuns(t.Context(), ExecutionDriverRunStaleTaskRecoveryCommand{
		WorkspaceKey: "WS", RequestID: "recover-1", RunID: "run-1", NodeID: "node-1", LeaseID: "lease-1",
		FencingToken: 7, StaleBefore: time.Now(), ErrorClass: "stale", ErrorMessage: "stale",
	}); !strings.Contains(err.Error(), "token") {
		t.Fatalf("RecoverStaleChildTaskRuns missing token error = %v", err)
	}
}

func TestExecutionTerminalDriverRunWorkRecoveryUsesSystemRouteAndStrictReceipt(t *testing.T) {
	recoveredAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/driver-runs/run-1/commands/recover-terminal-work" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Lease-Token"); got != "" {
			t.Fatalf("system recovery unexpectedly carried X-Lease-Token = %q", got)
		}
		var request struct {
			RequestID    string                 `json:"request_id"`
			ParentStatus domain.DriverRunStatus `json:"parent_status"`
			Reason       string                 `json:"reason"`
			ErrorClass   string                 `json:"error_class"`
			RecoveredAt  time.Time              `json:"recovered_at"`
		}
		decodeJSONBody(t, r, &request)
		if request.RequestID != "terminal-work-1" || request.ParentStatus != domain.DriverRunFailed ||
			request.Reason != "parent driver run became failed" || request.ErrorClass != "parent_run_terminal" ||
			!request.RecoveredAt.Equal(recoveredAt) {
			t.Fatalf("recovery request = %+v", request)
		}
		appliedAt := recoveredAt
		writeJSON(t, w, ExecutionTerminalDriverRunWorkRecoveryResult{
			WorkspaceKey: "WS", DriverRunID: "run-1", ParentStatus: domain.DriverRunFailed,
			Reason: "parent driver run became failed", ErrorClass: "parent_run_terminal", RecoveredAt: recoveredAt,
			RecoveredTaskRunIDs: []string{"task-run-1"}, ReleasedWorkItemIDs: []string{"TASK-1"},
			PreservedSuccessorWorkItemIDs: []string{}, ActionID: "action-1",
			Action: &ExecutionActionLedger{WorkspaceKey: "WS", ActionID: "action-1",
				ActionType: "recover_terminal_driver_run_work", Status: "applied", CreatedAt: recoveredAt, AppliedAt: &appliedAt},
		})
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Execution().RecoverTerminalDriverRunWork(t.Context(), ExecutionTerminalDriverRunWorkRecoveryCommand{
		WorkspaceKey: "WS", RequestID: "terminal-work-1", DriverRunID: "run-1",
		ParentStatus: domain.DriverRunFailed, Reason: "parent driver run became failed",
		ErrorClass: "parent_run_terminal", RecoveredAt: recoveredAt,
	})
	if err != nil || result.ActionID != "action-1" || len(result.RecoveredTaskRunIDs) != 1 || len(result.ReleasedWorkItemIDs) != 1 {
		t.Fatalf("RecoverTerminalDriverRunWork() = %#v, %v", result, err)
	}
}
