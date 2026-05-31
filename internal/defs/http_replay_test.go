package defs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestApplyRuntimeStateRecordsReconcilesExistingMutableRecordsThroughFleetDBHTTP(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	firstSeenAt := now.Add(time.Minute)
	secondSeenAt := now.Add(2 * time.Minute)
	endedAt := now.Add(3 * time.Minute)

	var commandCreated, terminalCreated, artifactCreated bool
	var commandCreateCount, terminalCreateCount, artifactCreateCount int
	var commandCompleteCount, terminalPatchCount, artifactPatchCount int
	command := domain.AgentCommand{}
	terminal := domain.TerminalSession{}
	artifact := domain.Artifact{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/HTTP/agent-commands/cmd-http":
			if !commandCreated {
				writeHTTPError(w, http.StatusNotFound, "missing command")
				return
			}
			writeHTTPJSON(t, w, command)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/HTTP/agent-commands":
			if commandCreated {
				t.Fatalf("unexpected command create on replay")
			}
			commandCreateCount++
			var body struct {
				CommandID     string            `json:"command_id"`
				TargetAgentID string            `json:"target_agent_id"`
				TargetNodeID  string            `json:"target_node_id"`
				SessionID     string            `json:"session_id"`
				Type          string            `json:"type"`
				Payload       map[string]string `json:"payload"`
				Status        string            `json:"status"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode command create: %v", err)
			}
			if body.CommandID != "cmd-http" || body.TargetAgentID != "runner" || body.TargetNodeID != "node-a" ||
				body.SessionID != "session-a" || body.Type != "start-task" || body.Payload["task_id"] != "TASK-1" {
				t.Fatalf("command create body = %+v", body)
			}
			if body.Status != "" {
				t.Fatalf("command create body included server-managed status: %+v", body)
			}
			commandCreated = true
			command = domain.AgentCommand{
				WorkspaceKey:  "HTTP",
				CommandID:     body.CommandID,
				Cursor:        42,
				TargetAgentID: body.TargetAgentID,
				TargetNodeID:  body.TargetNodeID,
				SessionID:     body.SessionID,
				Type:          body.Type,
				Payload:       body.Payload,
				Status:        domain.AgentCommandQueued,
				CreatedAt:     now,
				UpdatedAt:     now,
			}
			writeHTTPJSON(t, w, command)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/HTTP/agent-commands/cmd-http/complete":
			commandCompleteCount++
			var body store.AgentCommandComplete
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode command complete: %v", err)
			}
			command.Status = body.Status
			command.Result = body.Result
			command.ErrorClass = body.ErrorClass
			command.UpdatedAt = command.UpdatedAt.Add(time.Second)
			writeHTTPJSON(t, w, command)

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/HTTP/terminal-sessions/term-http":
			if !terminalCreated {
				writeHTTPError(w, http.StatusNotFound, "missing terminal")
				return
			}
			writeHTTPJSON(t, w, terminal)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/HTTP/terminal-sessions":
			if terminalCreated {
				t.Fatalf("unexpected terminal create on replay")
			}
			terminalCreateCount++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode terminal create: %v", err)
			}
			if body["terminal_id"] != "term-http" || body["agent_id"] != "runner" || body["node_id"] != "node-a" ||
				body["task_id"] != "TASK-1" || body["status"] != string(domain.TerminalSessionOpen) {
				t.Fatalf("terminal create body = %#v", body)
			}
			terminalCreated = true
			terminal = domain.TerminalSession{
				WorkspaceKey:    "HTTP",
				TerminalID:      "term-http",
				AgentID:         "runner",
				SessionID:       "session-a",
				NodeID:          "node-a",
				TaskID:          "TASK-1",
				Title:           "HTTP terminal",
				Kind:            "pty",
				Status:          domain.TerminalSessionOpen,
				PTYProvider:     "local",
				StreamRef:       "stream://term-http",
				TranscriptRef:   "transcript://term-http",
				AttachedClients: 1,
				Metadata:        map[string]string{"phase": "first"},
				StartedAt:       now,
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			writeHTTPJSON(t, w, terminal)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/HTTP/terminal-sessions/term-http":
			terminalPatchCount++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode terminal patch: %v", err)
			}
			if _, ok := body["AgentID"]; ok {
				t.Fatalf("terminal patch used Go field names: %#v", body)
			}
			terminal.AgentID = stringBody(body, "agent_id")
			terminal.SessionID = stringBody(body, "session_id")
			terminal.NodeID = stringBody(body, "node_id")
			terminal.TaskID = stringBody(body, "task_id")
			terminal.Title = stringBody(body, "title")
			terminal.Kind = stringBody(body, "kind")
			terminal.Status = domain.TerminalSessionStatus(stringBody(body, "status"))
			terminal.PTYProvider = stringBody(body, "pty_provider")
			terminal.StreamRef = stringBody(body, "stream_ref")
			terminal.TranscriptRef = stringBody(body, "transcript_ref")
			terminal.AttachedClients = int(body["attached_clients"].(float64))
			terminal.Metadata = stringMapBody(body, "metadata")
			if raw, ok := body["last_seen_at"].(string); ok && raw != "" {
				terminal.LastSeenAt = mustParseTime(t, raw)
			}
			if raw, ok := body["ended_at"].(string); ok && raw != "" {
				parsed := mustParseTime(t, raw)
				terminal.EndedAt = &parsed
			}
			terminal.UpdatedAt = terminal.UpdatedAt.Add(time.Second)
			writeHTTPJSON(t, w, terminal)

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/HTTP/artifacts/artifact-http":
			if !artifactCreated {
				writeHTTPError(w, http.StatusNotFound, "missing artifact")
				return
			}
			writeHTTPJSON(t, w, artifact)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/HTTP/artifacts":
			if artifactCreated {
				t.Fatalf("unexpected artifact create on replay")
			}
			artifactCreateCount++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode artifact create: %v", err)
			}
			if body["artifact_id"] != "artifact-http" || body["type"] != "report" || body["uri"] != "artifact://runner/report.json" {
				t.Fatalf("artifact create body = %#v", body)
			}
			artifactCreated = true
			artifact = domain.Artifact{
				WorkspaceKey: "HTTP",
				ArtifactID:   "artifact-http",
				AgentID:      "runner",
				SessionID:    "session-a",
				TerminalID:   "term-http",
				TaskID:       "TASK-1",
				Type:         "report",
				URI:          "artifact://runner/report.json",
				Summary:      "HTTP report",
				MIMEType:     "application/json",
				SizeBytes:    128,
				Checksum:     "sha256:first",
				Metadata:     map[string]string{"phase": "first"},
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			writeHTTPJSON(t, w, artifact)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/HTTP/artifacts/artifact-http":
			artifactPatchCount++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode artifact patch: %v", err)
			}
			if _, ok := body["ArtifactID"]; ok {
				t.Fatalf("artifact patch used Go field names: %#v", body)
			}
			artifact.AgentID = stringBody(body, "agent_id")
			artifact.SessionID = stringBody(body, "session_id")
			artifact.TerminalID = stringBody(body, "terminal_id")
			artifact.TaskID = stringBody(body, "task_id")
			artifact.Type = stringBody(body, "type")
			artifact.URI = stringBody(body, "uri")
			artifact.Summary = stringBody(body, "summary")
			artifact.MIMEType = stringBody(body, "mime_type")
			artifact.SizeBytes = int64(body["size_bytes"].(float64))
			artifact.Checksum = stringBody(body, "checksum")
			artifact.Metadata = stringMapBody(body, "metadata")
			artifact.UpdatedAt = artifact.UpdatedAt.Add(time.Second)
			writeHTTPJSON(t, w, artifact)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client, err := fleetdb.New(fleetdb.Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatalf("new fleetdb client: %v", err)
	}
	if err := Apply(ctx, client, "HTTP", "tester", runtimeReplayPlan("cmd-http", "term-http", "artifact-http", firstSeenAt, nil, "first")); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	if err := Apply(ctx, client, "HTTP", "tester", runtimeReplayPlan("cmd-http", "term-http", "artifact-http", secondSeenAt, &endedAt, "second")); err != nil {
		t.Fatalf("Apply(second) error = %v", err)
	}
	if commandCreateCount != 1 || terminalCreateCount != 1 || artifactCreateCount != 1 {
		t.Fatalf("create counts command=%d terminal=%d artifact=%d, want one create each", commandCreateCount, terminalCreateCount, artifactCreateCount)
	}
	if commandCompleteCount != 2 || terminalPatchCount != 2 || artifactPatchCount != 2 {
		t.Fatalf("update counts command=%d terminal=%d artifact=%d, want two updates each", commandCompleteCount, terminalPatchCount, artifactPatchCount)
	}
	if command.Cursor != 42 || !command.CreatedAt.Equal(now) || command.Status != domain.AgentCommandFailed ||
		command.Result != "failed" || command.ErrorClass != "provider_unavailable" {
		t.Fatalf("command = %+v, want HTTP replay to preserve cursor/created_at and update completion state", command)
	}
	if !terminal.CreatedAt.Equal(now) || terminal.AgentID != "runner-2" || terminal.Status != domain.TerminalSessionClosed ||
		terminal.Metadata["phase"] != "second" || !terminal.LastSeenAt.Equal(secondSeenAt) ||
		terminal.EndedAt == nil || !terminal.EndedAt.Equal(endedAt) {
		t.Fatalf("terminal = %+v, want HTTP replay to preserve created_at and update terminal state", terminal)
	}
	if !artifact.CreatedAt.Equal(now) || artifact.AgentID != "runner-2" || artifact.Type != "log" ||
		artifact.URI != "artifact://runner/report-v2.txt" || artifact.Metadata["phase"] != "second" {
		t.Fatalf("artifact = %+v, want HTTP replay to preserve created_at and update artifact state", artifact)
	}
}

func TestApplyControlPlaneRuntimeRecordsReconcilesExistingIDsThroughFleetDBHTTP(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 30, 14, 0, 0, 0, time.UTC)
	startedAt := now.Add(time.Minute)
	firstHeartbeat := now.Add(2 * time.Minute)
	secondHeartbeat := now.Add(3 * time.Minute)
	finishedAt := now.Add(4 * time.Minute)
	expiresAt := now.Add(time.Hour)

	var workflowCreated, taskCreated, sessionCreated bool
	var leaseCreated, ownershipCreated bool
	var workflowCreateCount, taskEnsureCount, sessionCreateCount int
	var leaseCreateCount, ownershipAcquireCount int
	var workflowPatchCount, taskPatchCount, sessionPatchCount int
	var leaseReleaseCount, ownershipReleaseCount int
	workflow := domain.WorkflowRun{}
	task := domain.TaskRun{}
	session := domain.AgentSession{}
	lease := domain.AgentLease{}
	ownership := domain.AgentOwnershipLease{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/HTTP/workflow-runs/wrun-http":
			if !workflowCreated {
				writeHTTPError(w, http.StatusNotFound, "missing workflow run")
				return
			}
			writeHTTPJSON(t, w, workflow)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/HTTP/workflow-runs":
			if workflowCreated {
				t.Fatalf("unexpected workflow run create on replay")
			}
			workflowCreateCount++
			var body struct {
				RunID           string                   `json:"run_id"`
				WorkflowName    string                   `json:"workflow_name"`
				WorkflowVersion string                   `json:"workflow_version"`
				BundleHash      string                   `json:"bundle_hash"`
				IdempotencyKey  string                   `json:"idempotency_key"`
				Input           json.RawMessage          `json:"input"`
				Status          domain.WorkflowRunStatus `json:"status"`
				LeaseOwner      string                   `json:"lease_owner"`
				LeaseToken      string                   `json:"lease_token"`
				StartedAt       time.Time                `json:"started_at"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode workflow run create: %v", err)
			}
			if body.RunID != "wrun-http" || body.WorkflowName != "daily-review" ||
				body.WorkflowVersion != "v1" || body.Status != domain.WorkflowRunRunning ||
				body.IdempotencyKey != "workflow-idem" || !body.StartedAt.Equal(startedAt) {
				t.Fatalf("workflow run create body = %+v", body)
			}
			workflowCreated = true
			workflow = domain.WorkflowRun{
				WorkspaceKey:    "HTTP",
				RunID:           body.RunID,
				WorkflowName:    body.WorkflowName,
				WorkflowVersion: body.WorkflowVersion,
				BundleHash:      body.BundleHash,
				IdempotencyKey:  body.IdempotencyKey,
				Input:           body.Input,
				Status:          body.Status,
				LeaseOwner:      body.LeaseOwner,
				LeaseToken:      "server-workflow-token",
				FencingToken:    101,
				StartedAt:       body.StartedAt,
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			writeHTTPJSON(t, w, workflow)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/HTTP/workflow-runs/wrun-http":
			workflowPatchCount++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode workflow run patch: %v", err)
			}
			if _, ok := body["RunID"]; ok {
				t.Fatalf("workflow run patch used Go field names: %#v", body)
			}
			workflow.Status = domain.WorkflowRunStatus(stringBody(body, "status"))
			if raw, ok := body["result"]; ok && raw != nil {
				result, err := json.Marshal(raw)
				if err != nil {
					t.Fatalf("marshal workflow result body: %v", err)
				}
				workflow.Result = result
			} else {
				workflow.Result = nil
			}
			workflow.ErrorClass = stringBody(body, "error_class")
			workflow.ErrorMessage = stringBody(body, "error_message")
			workflow.WaitCondition = stringBody(body, "wait_condition")
			workflow.LeaseOwner = stringBody(body, "lease_owner")
			workflow.LeaseToken = stringBody(body, "lease_token")
			if raw, ok := body["fencing_token"].(float64); ok {
				workflow.FencingToken = int64(raw)
			}
			if raw, ok := body["started_at"].(string); ok && raw != "" {
				workflow.StartedAt = mustParseTime(t, raw)
			}
			if raw, ok := body["finished_at"].(string); ok && raw != "" {
				parsed := mustParseTime(t, raw)
				workflow.FinishedAt = &parsed
			} else {
				workflow.FinishedAt = nil
			}
			workflow.UpdatedAt = workflow.UpdatedAt.Add(time.Second)
			writeHTTPJSON(t, w, workflow)

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/HTTP/task-runs/trun-http":
			if !taskCreated {
				writeHTTPError(w, http.StatusNotFound, "missing task run")
				return
			}
			writeHTTPJSON(t, w, task)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/HTTP/task-runs/ensure":
			if taskCreated {
				t.Fatalf("unexpected task run ensure on replay")
			}
			taskEnsureCount++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode task run ensure: %v", err)
			}
			if body["task_run_id"] != "trun-http" || body["workflow_run_id"] != "wrun-http" ||
				body["work_item_id"] != "TASK-1" || body["role_name"] != "builder" ||
				body["status"] != string(domain.TaskRunRunning) {
				t.Fatalf("task run ensure body = %#v", body)
			}
			taskCreated = true
			task = domain.TaskRun{
				WorkspaceKey:    "HTTP",
				TaskRunID:       "trun-http",
				IdempotencyKey:  stringBody(body, "idempotency_key"),
				WorkflowRunID:   "wrun-http",
				WorkItemID:      "TASK-1",
				RoleName:        "builder",
				ClaimActor:      stringBody(body, "claim_actor"),
				ClaimEventID:    stringBody(body, "claim_event_id"),
				Status:          domain.TaskRunStatus(stringBody(body, "status")),
				Attempt:         1,
				AgentID:         stringBody(body, "agent_id"),
				NodeID:          stringBody(body, "node_id"),
				CommandID:       stringBody(body, "command_id"),
				SessionID:       stringBody(body, "session_id"),
				LeaseID:         stringBody(body, "lease_id"),
				ParentSessionID: stringBody(body, "parent_session_id"),
				Reason:          stringBody(body, "reason"),
				StartedAt:       startedAt,
				Metadata:        stringMapBody(body, "metadata"),
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			writeHTTPJSON(t, w, task)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/HTTP/task-runs/trun-http":
			taskPatchCount++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode task run patch: %v", err)
			}
			if _, ok := body["TaskRunID"]; ok {
				t.Fatalf("task run patch used Go field names: %#v", body)
			}
			task.ClaimActor = stringBody(body, "claim_actor")
			task.ClaimEventID = stringBody(body, "claim_event_id")
			task.Status = domain.TaskRunStatus(stringBody(body, "status"))
			task.AgentID = stringBody(body, "agent_id")
			task.NodeID = stringBody(body, "node_id")
			task.CommandID = stringBody(body, "command_id")
			task.SessionID = stringBody(body, "session_id")
			task.LeaseID = stringBody(body, "lease_id")
			task.ParentSessionID = stringBody(body, "parent_session_id")
			task.Reason = stringBody(body, "reason")
			task.ErrorClass = stringBody(body, "error_class")
			task.ErrorMessage = stringBody(body, "error_message")
			task.Metadata = stringMapBody(body, "metadata")
			if raw, ok := body["started_at"].(string); ok && raw != "" {
				task.StartedAt = mustParseTime(t, raw)
			}
			if raw, ok := body["finished_at"].(string); ok && raw != "" {
				parsed := mustParseTime(t, raw)
				task.FinishedAt = &parsed
			} else {
				task.FinishedAt = nil
			}
			task.UpdatedAt = task.UpdatedAt.Add(time.Second)
			writeHTTPJSON(t, w, task)

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/HTTP/agent-sessions/session-http":
			if !sessionCreated {
				writeHTTPError(w, http.StatusNotFound, "missing agent session")
				return
			}
			writeHTTPJSON(t, w, session)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/HTTP/agent-sessions":
			if sessionCreated {
				t.Fatalf("unexpected agent session create on replay")
			}
			sessionCreateCount++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode agent session create: %v", err)
			}
			if body["session_id"] != "session-http" || body["agent_id"] != "runner" ||
				body["kind"] != string(domain.AgentSessionKindTask) ||
				body["status"] != string(domain.AgentSessionRunning) {
				t.Fatalf("agent session create body = %#v", body)
			}
			sessionCreated = true
			session = domain.AgentSession{
				WorkspaceKey:    "HTTP",
				SessionID:       "session-http",
				AgentID:         "runner",
				NodeID:          stringBody(body, "node_id"),
				Kind:            domain.AgentSessionKind(stringBody(body, "kind")),
				TaskID:          stringBody(body, "task_id"),
				TerminalID:      stringBody(body, "terminal_id"),
				ParentSessionID: stringBody(body, "parent_session_id"),
				Status:          domain.AgentSessionStatus(stringBody(body, "status")),
				Phase:           stringBody(body, "phase"),
				Attempt:         int(body["attempt"].(float64)),
				StartedAt:       startedAt,
				LastHeartbeat:   firstHeartbeat,
				Metadata:        stringMapBody(body, "metadata"),
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			writeHTTPJSON(t, w, session)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/HTTP/agent-sessions/session-http":
			sessionPatchCount++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode agent session patch: %v", err)
			}
			if _, ok := body["SessionID"]; ok {
				t.Fatalf("agent session patch used Go field names: %#v", body)
			}
			session.NodeID = stringBody(body, "node_id")
			session.TaskID = stringBody(body, "task_id")
			session.Status = domain.AgentSessionStatus(stringBody(body, "status"))
			session.Phase = stringBody(body, "phase")
			session.Summary = stringBody(body, "summary")
			session.ErrorClass = stringBody(body, "error_class")
			session.Metadata = stringMapBody(body, "metadata")
			if raw, ok := body["last_heartbeat"].(string); ok && raw != "" {
				session.LastHeartbeat = mustParseTime(t, raw)
			}
			if raw, ok := body["finished_at"].(string); ok && raw != "" {
				parsed := mustParseTime(t, raw)
				session.FinishedAt = &parsed
			} else {
				session.FinishedAt = nil
			}
			if raw, ok := body["exit_code"].(float64); ok {
				exitCode := int(raw)
				session.ExitCode = &exitCode
			} else {
				session.ExitCode = nil
			}
			session.UpdatedAt = session.UpdatedAt.Add(time.Second)
			writeHTTPJSON(t, w, session)

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/HTTP/agent-leases/lease-http":
			if !leaseCreated {
				writeHTTPError(w, http.StatusNotFound, "missing agent lease")
				return
			}
			writeHTTPJSON(t, w, lease)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/HTTP/agent-sessions/session-http/leases":
			if leaseCreated {
				t.Fatalf("unexpected agent lease create on replay")
			}
			leaseCreateCount++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode agent lease create: %v", err)
			}
			if body["lease_id"] != "lease-http" || body["agent_id"] != "runner" ||
				body["node_id"] != "node-a" || body["token"] != nil || body["fencing_token"] != nil {
				t.Fatalf("agent lease create body = %#v", body)
			}
			leaseCreated = true
			lease = domain.AgentLease{
				WorkspaceKey:  "HTTP",
				LeaseID:       "lease-http",
				SessionID:     "session-http",
				AgentID:       "runner",
				NodeID:        "node-a",
				Token:         "server-lease-token",
				FencingToken:  17,
				Status:        domain.AgentLeaseActive,
				ExpiresAt:     expiresAt,
				LastHeartbeat: firstHeartbeat,
				CreatedAt:     now,
				UpdatedAt:     now,
			}
			writeHTTPJSON(t, w, lease)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/HTTP/agent-leases/lease-http/release":
			leaseReleaseCount++
			if got := r.Header.Get("X-Agent-Lease-Token"); got != "server-lease-token" {
				t.Fatalf("agent lease release token = %q, want server token", got)
			}
			lease.Status = domain.AgentLeaseReleased
			lease.UpdatedAt = lease.UpdatedAt.Add(time.Second)
			writeHTTPJSON(t, w, lease)

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/HTTP/agent-ownership-leases/runner":
			if !ownershipCreated {
				writeHTTPError(w, http.StatusNotFound, "missing ownership lease")
				return
			}
			writeHTTPJSON(t, w, ownership)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/HTTP/agent-ownership-leases/runner/acquire":
			if ownershipCreated {
				t.Fatalf("unexpected ownership lease acquire on replay")
			}
			ownershipAcquireCount++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode ownership lease acquire: %v", err)
			}
			if body["lease_id"] != "ownership-http" || body["owner_id"] != "owner-a" ||
				body["runtime_provider"] != string(domain.RuntimeProviderLocal) ||
				body["node_id"] != "node-a" || body["token"] != nil || body["fencing_token"] != nil {
				t.Fatalf("ownership lease acquire body = %#v", body)
			}
			ownershipCreated = true
			ownership = domain.AgentOwnershipLease{
				WorkspaceKey:    "HTTP",
				AgentID:         "runner",
				LeaseID:         "ownership-http",
				OwnerID:         "owner-a",
				RuntimeProvider: domain.RuntimeProviderLocal,
				NodeID:          "node-a",
				Token:           "server-ownership-token",
				FencingToken:    29,
				Status:          domain.AgentLeaseActive,
				ExpiresAt:       expiresAt,
				LastHeartbeat:   firstHeartbeat,
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			writeHTTPJSON(t, w, ownership)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/HTTP/agent-ownership-leases/runner/release":
			ownershipReleaseCount++
			if got := r.Header.Get("X-Agent-Ownership-Lease-Token"); got != "server-ownership-token" {
				t.Fatalf("ownership lease release token = %q, want server token", got)
			}
			ownership.Status = domain.AgentLeaseReleased
			ownership.UpdatedAt = ownership.UpdatedAt.Add(time.Second)
			writeHTTPJSON(t, w, ownership)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client, err := fleetdb.New(fleetdb.Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatalf("new fleetdb client: %v", err)
	}
	if err := Apply(ctx, client, "HTTP", "tester", controlPlaneReplayPlan(startedAt, firstHeartbeat, nil, expiresAt, "first")); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	if err := Apply(ctx, client, "HTTP", "tester", controlPlaneReplayPlan(startedAt, secondHeartbeat, &finishedAt, expiresAt, "second")); err != nil {
		t.Fatalf("Apply(second) error = %v", err)
	}

	if workflowCreateCount != 1 || taskEnsureCount != 1 || sessionCreateCount != 1 ||
		leaseCreateCount != 1 || ownershipAcquireCount != 1 {
		t.Fatalf("create counts workflow=%d task=%d session=%d lease=%d ownership=%d, want one create/acquire each",
			workflowCreateCount, taskEnsureCount, sessionCreateCount, leaseCreateCount, ownershipAcquireCount)
	}
	if workflowPatchCount != 2 || taskPatchCount != 2 || sessionPatchCount != 2 {
		t.Fatalf("patch counts workflow=%d task=%d session=%d, want two patches each",
			workflowPatchCount, taskPatchCount, sessionPatchCount)
	}
	if leaseReleaseCount != 1 || ownershipReleaseCount != 1 {
		t.Fatalf("release counts lease=%d ownership=%d, want one release each", leaseReleaseCount, ownershipReleaseCount)
	}
	if !workflow.CreatedAt.Equal(now) || workflow.Status != domain.WorkflowRunCompleted ||
		string(workflow.Result) != `{"ok":true,"phase":"second"}` ||
		workflow.FencingToken != 8 || workflow.FinishedAt == nil || !workflow.FinishedAt.Equal(finishedAt) {
		t.Fatalf("workflow = %+v, want replay to preserve created_at and update mutable run state", workflow)
	}
	if !task.CreatedAt.Equal(now) || task.Status != domain.TaskRunFailed ||
		task.AgentID != "runner-2" || task.Metadata["phase"] != "second" ||
		task.FinishedAt == nil || !task.FinishedAt.Equal(finishedAt) {
		t.Fatalf("task = %+v, want replay to preserve created_at and update mutable task state", task)
	}
	if !session.CreatedAt.Equal(now) || session.Status != domain.AgentSessionFailed ||
		session.NodeID != "node-b" || session.Summary != "failed during verification" ||
		session.ExitCode == nil || *session.ExitCode != 42 ||
		!session.LastHeartbeat.Equal(secondHeartbeat) ||
		session.FinishedAt == nil || !session.FinishedAt.Equal(finishedAt) {
		t.Fatalf("session = %+v, want replay to preserve created_at and update mutable session state", session)
	}
	if !lease.CreatedAt.Equal(now) || lease.Token != "server-lease-token" ||
		lease.FencingToken != 17 || lease.Status != domain.AgentLeaseReleased {
		t.Fatalf("lease = %+v, want replay to preserve server token/fencing and release existing lease", lease)
	}
	if !ownership.CreatedAt.Equal(now) || ownership.Token != "server-ownership-token" ||
		ownership.FencingToken != 29 || ownership.Status != domain.AgentLeaseReleased {
		t.Fatalf("ownership = %+v, want replay to preserve server token/fencing and release existing ownership lease", ownership)
	}
}

func runtimeReplayPlan(commandID, terminalID, artifactID string, seenAt time.Time, endedAt *time.Time, phase string) *Plan {
	second := phase == "second"
	commandStatus := domain.AgentCommandSucceeded
	commandResult := "started"
	commandError := ""
	terminalStatus := domain.TerminalSessionOpen
	agentID := "runner"
	sessionID := "session-a"
	nodeID := "node-a"
	taskID := "TASK-1"
	title := "HTTP terminal"
	streamRef := "stream://term-http"
	transcriptRef := "transcript://term-http"
	attachedClients := 1
	artifactType := "report"
	artifactURI := "artifact://runner/report.json"
	artifactSummary := "HTTP report"
	mimeType := "application/json"
	sizeBytes := int64(128)
	checksum := "sha256:first"
	if second {
		commandStatus = domain.AgentCommandFailed
		commandResult = "failed"
		commandError = "provider_unavailable"
		terminalStatus = domain.TerminalSessionClosed
		agentID = "runner-2"
		sessionID = "session-b"
		nodeID = "node-b"
		taskID = "TASK-2"
		title = "Updated HTTP terminal"
		streamRef = "stream://term-http-v2"
		transcriptRef = "transcript://term-http-v2"
		attachedClients = 3
		artifactType = "log"
		artifactURI = "artifact://runner/report-v2.txt"
		artifactSummary = "updated HTTP report"
		mimeType = "text/plain"
		sizeBytes = 256
		checksum = "sha256:second"
	}
	return &Plan{
		AgentCommands: []AgentCommandModule{{
			CommandID:     commandID,
			SourcePath:    "state/commands.json",
			SourceHash:    "hash-" + phase,
			Version:       "state-" + phase,
			TargetAgentID: "runner",
			TargetNodeID:  "node-a",
			SessionID:     "session-a",
			Type:          "start-task",
			Payload:       map[string]string{"task_id": "TASK-1"},
			Status:        commandStatus,
			Result:        commandResult,
			ErrorClass:    commandError,
		}},
		TerminalSessions: []TerminalSessionModule{{
			TerminalID:      terminalID,
			SourcePath:      "state/terminals.json",
			SourceHash:      "hash-" + phase,
			Version:         "state-" + phase,
			AgentID:         agentID,
			SessionID:       sessionID,
			NodeID:          nodeID,
			TaskID:          taskID,
			Title:           title,
			Kind:            "pty",
			Status:          terminalStatus,
			PTYProvider:     "local",
			StreamRef:       streamRef,
			TranscriptRef:   transcriptRef,
			AttachedClients: attachedClients,
			LastSeenAt:      &seenAt,
			EndedAt:         endedAt,
			Metadata:        map[string]string{"phase": phase},
		}},
		Artifacts: []ArtifactModule{{
			ArtifactID: artifactID,
			SourcePath: "state/artifacts.json",
			SourceHash: "hash-" + phase,
			Version:    "state-" + phase,
			AgentID:    agentID,
			SessionID:  sessionID,
			TerminalID: terminalID,
			TaskID:     taskID,
			Type:       artifactType,
			URI:        artifactURI,
			Summary:    artifactSummary,
			MIMEType:   mimeType,
			SizeBytes:  sizeBytes,
			Checksum:   checksum,
			Metadata:   map[string]string{"phase": phase},
		}},
	}
}

func controlPlaneReplayPlan(startedAt, heartbeat time.Time, finishedAt *time.Time, expiresAt time.Time, phase string) *Plan {
	second := phase == "second"
	workflowStatus := domain.WorkflowRunRunning
	workflowResult := json.RawMessage(nil)
	waitCondition := "waiting-for-task"
	workflowErrorClass := ""
	workflowErrorMessage := ""
	workflowLeaseToken := "workflow-token-first"
	workflowFencingToken := int64(7)
	taskStatus := domain.TaskRunRunning
	taskAgentID := "runner"
	taskNodeID := "node-a"
	taskCommandID := "cmd-http"
	taskReason := "started"
	taskErrorClass := ""
	taskErrorMessage := ""
	sessionStatus := domain.AgentSessionRunning
	sessionPhase := "working"
	sessionNodeID := "node-a"
	sessionSummary := ""
	sessionErrorClass := ""
	var exitCode *int
	leaseStatus := domain.AgentLeaseActive
	if second {
		workflowStatus = domain.WorkflowRunCompleted
		workflowResult = json.RawMessage(`{"ok":true,"phase":"second"}`)
		waitCondition = ""
		workflowLeaseToken = "workflow-token-second"
		workflowFencingToken = 8
		taskStatus = domain.TaskRunFailed
		taskAgentID = "runner-2"
		taskNodeID = "node-b"
		taskCommandID = "cmd-http-2"
		taskReason = "verification failed"
		taskErrorClass = "verification_failed"
		taskErrorMessage = "focused test failed"
		sessionStatus = domain.AgentSessionFailed
		sessionPhase = "failed"
		sessionNodeID = "node-b"
		sessionSummary = "failed during verification"
		sessionErrorClass = "verification_failed"
		code := 42
		exitCode = &code
		leaseStatus = domain.AgentLeaseReleased
	}
	return &Plan{
		WorkflowRuns: []WorkflowRunModule{{
			RunID:           "wrun-http",
			WorkflowName:    "daily-review",
			WorkflowVersion: "v1",
			SourcePath:      "state/workflow-runs.json",
			SourceHash:      "hash-" + phase,
			Version:         "state-" + phase,
			BundleHash:      "bundle-http",
			IdempotencyKey:  "workflow-idem",
			Input:           json.RawMessage(`{"phase":"first"}`),
			Status:          workflowStatus,
			Result:          workflowResult,
			ErrorClass:      workflowErrorClass,
			ErrorMessage:    workflowErrorMessage,
			WaitCondition:   waitCondition,
			LeaseOwner:      "runner",
			LeaseToken:      workflowLeaseToken,
			FencingToken:    workflowFencingToken,
			StartedAt:       &startedAt,
			FinishedAt:      finishedAt,
		}},
		TaskRuns: []TaskRunModule{{
			TaskRunID:       "trun-http",
			IdempotencyKey:  "task-idem",
			WorkflowRunID:   "wrun-http",
			WorkItemID:      "TASK-1",
			RoleName:        "builder",
			SourcePath:      "state/task-runs.json",
			SourceHash:      "hash-" + phase,
			Version:         "state-" + phase,
			ClaimActor:      taskAgentID,
			ClaimEventID:    "claim-" + phase,
			Status:          taskStatus,
			AgentID:         taskAgentID,
			NodeID:          taskNodeID,
			CommandID:       taskCommandID,
			SessionID:       "session-http",
			LeaseID:         "lease-http",
			ParentSessionID: "parent-session",
			Reason:          taskReason,
			StartedAt:       &startedAt,
			FinishedAt:      finishedAt,
			ErrorClass:      taskErrorClass,
			ErrorMessage:    taskErrorMessage,
			Metadata:        map[string]string{"phase": phase},
		}},
		AgentSessions: []AgentSessionModule{{
			SessionID:       "session-http",
			AgentID:         "runner",
			SourcePath:      "state/agent-sessions.json",
			SourceHash:      "hash-" + phase,
			Version:         "state-" + phase,
			NodeID:          sessionNodeID,
			Kind:            domain.AgentSessionKindTask,
			TaskID:          "TASK-1",
			TerminalID:      "terminal-http",
			ParentSessionID: "parent-session",
			Status:          sessionStatus,
			Phase:           sessionPhase,
			Attempt:         1,
			LastHeartbeat:   &heartbeat,
			FinishedAt:      finishedAt,
			Summary:         sessionSummary,
			ErrorClass:      sessionErrorClass,
			ExitCode:        exitCode,
			Metadata:        map[string]string{"phase": phase},
		}},
		AgentLeases: []AgentLeaseModule{{
			LeaseID:       "lease-http",
			SessionID:     "session-http",
			SourcePath:    "state/agent-leases.json",
			SourceHash:    "hash-" + phase,
			Version:       "state-" + phase,
			AgentID:       "runner",
			NodeID:        "node-a",
			Token:         "imported-lease-token",
			FencingToken:  99,
			Status:        leaseStatus,
			ExpiresAt:     &expiresAt,
			LastHeartbeat: &heartbeat,
			CreatedAt:     &startedAt,
			UpdatedAt:     &heartbeat,
		}},
		AgentOwnershipLeases: []AgentOwnershipLeaseModule{{
			AgentID:         "runner",
			LeaseID:         "ownership-http",
			OwnerID:         "owner-a",
			SourcePath:      "state/agent-ownership-leases.json",
			SourceHash:      "hash-" + phase,
			Version:         "state-" + phase,
			RuntimeProvider: domain.RuntimeProviderLocal,
			NodeID:          "node-a",
			Token:           "imported-ownership-token",
			FencingToken:    199,
			Status:          leaseStatus,
			ExpiresAt:       &expiresAt,
			LastHeartbeat:   &heartbeat,
			CreatedAt:       &startedAt,
			UpdatedAt:       &heartbeat,
		}},
	}
}

func writeHTTPJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func writeHTTPError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + message + `"}`))
}

func stringBody(body map[string]any, key string) string {
	value, _ := body[key].(string)
	return value
}

func stringMapBody(body map[string]any, key string) map[string]string {
	raw, _ := body[key].(map[string]any)
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k], _ = v.(string)
	}
	return out
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}
