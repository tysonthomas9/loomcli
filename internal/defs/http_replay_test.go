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
