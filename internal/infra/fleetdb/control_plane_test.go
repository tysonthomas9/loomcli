package fleetdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestControlPlaneClientNodeLifecycle(t *testing.T) {
	now := time.Now().UTC()
	var sawCreate bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/nodes":
			sawCreate = true
			var req struct {
				NodeID     string `json:"node_id"`
				TTLSeconds int    `json:"ttl_seconds"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode create: %v", err)
			}
			if req.NodeID != "node-1" || req.TTLSeconds != 30 {
				t.Fatalf("create body = %+v", req)
			}
			writeJSON(t, w, domain.Node{WorkspaceKey: "WS", NodeID: req.NodeID, LastHeartbeat: now, ExpiresAt: now.Add(30 * time.Second), CreatedAt: now, UpdatedAt: now})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/nodes/node-1/heartbeat":
			if r.URL.Query().Get("ttl_seconds") != "45" {
				t.Fatalf("ttl_seconds = %q", r.URL.Query().Get("ttl_seconds"))
			}
			writeJSON(t, w, domain.Node{WorkspaceKey: "WS", NodeID: "node-1", LastHeartbeat: now, ExpiresAt: now.Add(45 * time.Second), CreatedAt: now, UpdatedAt: now})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	node, err := client.Nodes().Create(t.Context(), store.NodeCreate{WorkspaceKey: "WS", NodeID: "node-1", TTL: 30 * time.Second})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	if !sawCreate || node.NodeID != "node-1" {
		t.Fatalf("node = %+v sawCreate=%v", node, sawCreate)
	}
	if _, err := client.Nodes().Heartbeat(t.Context(), "WS", "node-1", 45*time.Second); err != nil {
		t.Fatalf("heartbeat node: %v", err)
	}
}

func TestControlPlaneClientAgentSessionListQuery(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/WS/agent-sessions" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		q := r.URL.Query()
		if q.Get("agent_id") != "agent-1" || q.Get("node_id") != "node-1" || q.Get("task_id") != "T-1" || q.Get("status") != "running" || q.Get("limit") != "2" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		writeJSON(t, w, map[string]any{"agent_sessions": []domain.AgentSession{{WorkspaceKey: "WS", SessionID: "sess-1", AgentID: "agent-1"}}})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := client.AgentSessions().List(t.Context(), "WS", store.AgentSessionFilter{
		AgentID: "agent-1",
		NodeID:  "node-1",
		TaskID:  "T-1",
		Status:  domain.AgentSessionRunning,
		Limit:   2,
	})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "sess-1" {
		t.Fatalf("sessions = %+v", sessions)
	}
}

// TestAgentSessionList_FiltersKindAndParentClientSide guards the client-side
// filter applied for Kind + ParentSessionID. fleet-db's listAgentSessions
// doesn't accept those as query params yet, so the loomcli client must NOT
// send them on the wire and must filter the response locally. Without this
// the filter would silently no-op and callers would get the full session
// list back.
func TestAgentSessionList_FiltersKindAndParentClientSide(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/WS/agent-sessions" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		q := r.URL.Query()
		for _, forbidden := range []string{"kind", "parent_session_id"} {
			if q.Has(forbidden) {
				t.Fatalf("query contains unsupported filter %q=%q; client must filter locally",
					forbidden, q.Get(forbidden))
			}
		}
		if q.Has("limit") {
			t.Fatalf("limit must not be set when client-side kind/parent filter is active; got %q", q.Get("limit"))
		}
		writeJSON(t, w, map[string]any{"agent_sessions": []domain.AgentSession{
			{WorkspaceKey: "WS", SessionID: "orch-1", Kind: domain.AgentSessionKindOrchestration},
			{WorkspaceKey: "WS", SessionID: "task-a", Kind: domain.AgentSessionKindTask, ParentSessionID: "orch-1"},
			{WorkspaceKey: "WS", SessionID: "task-b", Kind: domain.AgentSessionKindTask, ParentSessionID: "orch-1"},
			{WorkspaceKey: "WS", SessionID: "task-c", Kind: domain.AgentSessionKindTask, ParentSessionID: "orch-other"},
		}})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.AgentSessions().List(t.Context(), "WS", store.AgentSessionFilter{
		Kind:            domain.AgentSessionKindTask,
		ParentSessionID: "orch-1",
		Limit:           5,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("filtered len = %d, want 2; got %+v", len(got), got)
	}
	for _, s := range got {
		if s.Kind != domain.AgentSessionKindTask || s.ParentSessionID != "orch-1" {
			t.Fatalf("unexpected session passed filter: %+v", s)
		}
	}
}

func TestControlPlaneClientAgentSessionUpdateBodyUsesWireNames(t *testing.T) {
	finishedAt := time.Now().UTC()
	exitCode := 7
	finishedAtPtr := &finishedAt
	exitCodePtr := &exitCode
	status := domain.AgentSessionFailed
	taskID := "T-1"
	errClass := "Fatal"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/WS/agent-sessions/sess-1" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode update: %v", err)
		}
		if _, ok := body["Status"]; ok {
			t.Fatalf("body contains Go field name Status: %#v", body)
		}
		if _, ok := body["NodeID"]; ok {
			t.Fatalf("body contains nil Go field NodeID: %#v", body)
		}
		if _, ok := body["node_id"]; ok {
			t.Fatalf("body contains nil wire field node_id: %#v", body)
		}
		if body["task_id"] != "T-1" || body["status"] != "failed" || body["error_class"] != "Fatal" || body["exit_code"] != float64(7) {
			t.Fatalf("body = %#v", body)
		}
		if body["finished_at"] == nil {
			t.Fatalf("body missing finished_at: %#v", body)
		}
		writeJSON(t, w, domain.AgentSession{
			WorkspaceKey: "WS",
			SessionID:    "sess-1",
			AgentID:      "agent-1",
			TaskID:       "T-1",
			Status:       domain.AgentSessionFailed,
			ExitCode:     &exitCode,
			FinishedAt:   &finishedAt,
		})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.AgentSessions().Update(t.Context(), "WS", "sess-1", store.AgentSessionUpdate{
		TaskID:     &taskID,
		Status:     &status,
		FinishedAt: &finishedAtPtr,
		ErrorClass: &errClass,
		ExitCode:   &exitCodePtr,
	})
	if err != nil {
		t.Fatalf("update session: %v", err)
	}
	if session.TaskID != "T-1" || session.Status != domain.AgentSessionFailed {
		t.Fatalf("session = %+v", session)
	}
}

func TestControlPlaneClientAgentCommandCreateOmitsUnsupportedStatus(t *testing.T) {
	now := time.Now().UTC()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/agent-commands" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode create: %v", err)
		}
		if _, ok := body["status"]; ok {
			t.Fatalf("create body contains unsupported status field: %#v", body)
		}
		if body["target_agent_id"] != "agent-1" || body["target_node_id"] != "node-1" || body["type"] != "start" {
			t.Fatalf("body = %#v", body)
		}
		writeJSON(t, w, domain.AgentCommand{
			WorkspaceKey:  "WS",
			CommandID:     "cmd-1",
			TargetAgentID: "agent-1",
			TargetNodeID:  "node-1",
			Type:          "start",
			Status:        domain.AgentCommandQueued,
			CreatedAt:     now,
			UpdatedAt:     now,
		})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := client.AgentCommands().Create(t.Context(), store.AgentCommandCreate{
		WorkspaceKey:  "WS",
		TargetAgentID: "agent-1",
		TargetNodeID:  "node-1",
		Type:          "start",
	})
	if err != nil {
		t.Fatalf("create command: %v", err)
	}
	if cmd.Status != domain.AgentCommandQueued {
		t.Fatalf("status = %q, want queued", cmd.Status)
	}
}

func TestControlPlaneClientNodeGetListAndUpdate(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/nodes/node-1":
			writeJSON(t, w, domain.Node{WorkspaceKey: "WS", NodeID: "node-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/nodes":
			writeJSON(t, w, map[string]any{"nodes": []domain.Node{{WorkspaceKey: "WS", NodeID: "node-1"}}})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/WS/nodes/node-1":
			body := decodeBody(t, r)
			if body["owner_actor"] != "owner" ||
				body["runtime_provider"] != "ci" ||
				body["version"] != "v2" ||
				body["capacity"] != float64(7) ||
				body["drain_state"] != "draining" ||
				body["expires_at"] == nil {
				t.Fatalf("node patch body = %#v", body)
			}
			writeJSON(t, w, domain.Node{WorkspaceKey: "WS", NodeID: "node-1", OwnerActor: "owner"})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if node, err := client.Nodes().Get(t.Context(), "WS", "node-1"); err != nil || node.NodeID != "node-1" {
		t.Fatalf("get node = %+v err=%v", node, err)
	}
	if nodes, err := client.Nodes().List(t.Context(), "WS"); err != nil || len(nodes) != 1 {
		t.Fatalf("list nodes = %+v err=%v", nodes, err)
	}
	owner := "owner"
	provider := domain.RuntimeProviderCI
	labels := []string{"a"}
	capabilities := []string{"shell"}
	tools := []string{"git"}
	version := "v2"
	capacity := 7
	drain := domain.NodeDrainDraining
	if _, err := client.Nodes().Update(t.Context(), "WS", "node-1", store.NodeUpdate{
		OwnerActor: &owner, RuntimeProvider: &provider, Labels: &labels,
		Capabilities: &capabilities, ToolInventory: &tools, Version: &version,
		Capacity: &capacity, DrainState: &drain, ExpiresAt: &expiresAt,
	}); err != nil {
		t.Fatalf("update node: %v", err)
	}
}

func TestControlPlaneClientTerminalSessions(t *testing.T) {
	endedAt := time.Now().UTC()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/terminal-sessions":
			body := decodeBody(t, r)
			if body["terminal_id"] != "term-1" || body["attached_clients"] != float64(2) {
				t.Fatalf("terminal create body = %#v", body)
			}
			writeJSON(t, w, domain.TerminalSession{WorkspaceKey: "WS", TerminalID: "term-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/terminal-sessions/term-1":
			writeJSON(t, w, domain.TerminalSession{WorkspaceKey: "WS", TerminalID: "term-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/terminal-sessions":
			q := r.URL.Query()
			if q.Get("agent_id") != "agent-1" || q.Get("session_id") != "sess-1" ||
				q.Get("node_id") != "node-1" || q.Get("task_id") != "task-1" ||
				q.Get("status") != "open" || q.Get("limit") != "3" {
				t.Fatalf("terminal list query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"terminal_sessions": []domain.TerminalSession{{WorkspaceKey: "WS", TerminalID: "term-1"}}})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/WS/terminal-sessions/term-1":
			body := decodeBody(t, r)
			if body["agent_id"] != "agent-2" || body["session_id"] != "sess-2" ||
				body["node_id"] != "node-2" || body["task_id"] != "task-2" ||
				body["title"] != "Terminal" || body["kind"] != "lead" ||
				body["status"] != "closed" || body["pty_provider"] != "tmux" ||
				body["stream_ref"] != "stream" || body["transcript_ref"] != "transcript" ||
				body["attached_clients"] != float64(1) || body["last_seen_at"] == nil ||
				body["ended_at"] == nil || body["metadata"] == nil {
				t.Fatalf("terminal patch body = %#v", body)
			}
			writeJSON(t, w, domain.TerminalSession{WorkspaceKey: "WS", TerminalID: "term-1", Status: domain.TerminalSessionClosed})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.TerminalSessions().Create(t.Context(), store.TerminalSessionCreate{
		WorkspaceKey: "WS", TerminalID: "term-1", AgentID: "agent-1",
		SessionID: "sess-1", NodeID: "node-1", TaskID: "task-1", Title: "Terminal",
		Kind: "lead", Status: domain.TerminalSessionOpen, PTYProvider: "tmux",
		StreamRef: "stream", TranscriptRef: "transcript", AttachedClients: 2,
		Metadata: map[string]string{"k": "v"},
	}); err != nil {
		t.Fatalf("create terminal: %v", err)
	}
	if _, err := client.TerminalSessions().Get(t.Context(), "WS", "term-1"); err != nil {
		t.Fatalf("get terminal: %v", err)
	}
	if got, err := client.TerminalSessions().List(t.Context(), "WS", store.TerminalSessionFilter{
		AgentID: "agent-1", SessionID: "sess-1", NodeID: "node-1", TaskID: "task-1",
		Status: domain.TerminalSessionOpen, Limit: 3,
	}); err != nil || len(got) != 1 {
		t.Fatalf("list terminal sessions = %+v err=%v", got, err)
	}
	agentID := "agent-2"
	sessionID := "sess-2"
	nodeID := "node-2"
	taskID := "task-2"
	title := "Terminal"
	kind := "lead"
	status := domain.TerminalSessionClosed
	provider := "tmux"
	stream := "stream"
	transcript := "transcript"
	clients := 1
	lastSeen := time.Now().UTC()
	endedPtr := &endedAt
	meta := map[string]string{"k": "v"}
	if _, err := client.TerminalSessions().Update(t.Context(), "WS", "term-1", store.TerminalSessionUpdate{
		AgentID: &agentID, SessionID: &sessionID, NodeID: &nodeID, TaskID: &taskID,
		Title: &title, Kind: &kind, Status: &status, PTYProvider: &provider,
		StreamRef: &stream, TranscriptRef: &transcript, AttachedClients: &clients,
		LastSeenAt: &lastSeen, EndedAt: &endedPtr, Metadata: &meta,
	}); err != nil {
		t.Fatalf("update terminal: %v", err)
	}
}

func TestControlPlaneClientArtifacts(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/artifacts":
			body := decodeBody(t, r)
			if body["artifact_id"] != "artifact-1" || body["type"] != "log" || body["size_bytes"] != float64(42) {
				t.Fatalf("artifact create body = %#v", body)
			}
			writeJSON(t, w, domain.Artifact{WorkspaceKey: "WS", ArtifactID: "artifact-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/artifacts/artifact-1":
			writeJSON(t, w, domain.Artifact{WorkspaceKey: "WS", ArtifactID: "artifact-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/artifacts":
			q := r.URL.Query()
			if q.Get("agent_id") != "agent-1" || q.Get("session_id") != "sess-1" ||
				q.Get("terminal_id") != "term-1" || q.Get("task_id") != "task-1" ||
				q.Get("type") != "log" || q.Get("limit") != "2" {
				t.Fatalf("artifact list query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"artifacts": []domain.Artifact{{WorkspaceKey: "WS", ArtifactID: "artifact-1"}}})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/WS/artifacts/artifact-1":
			body := decodeBody(t, r)
			if body["agent_id"] != "agent-2" || body["session_id"] != "sess-2" ||
				body["terminal_id"] != "term-2" || body["task_id"] != "task-2" ||
				body["type"] != "transcript" || body["uri"] != "file:///new" ||
				body["summary"] != "updated" || body["mime_type"] != "text/plain" ||
				body["size_bytes"] != float64(99) || body["checksum"] != "sha256:def" ||
				body["metadata"] == nil {
				t.Fatalf("artifact patch body = %#v", body)
			}
			writeJSON(t, w, domain.Artifact{WorkspaceKey: "WS", ArtifactID: "artifact-1", URI: "file:///new"})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Artifacts().Create(t.Context(), store.ArtifactCreate{
		WorkspaceKey: "WS", ArtifactID: "artifact-1", AgentID: "agent-1",
		SessionID: "sess-1", TerminalID: "term-1", TaskID: "task-1",
		Type: "log", URI: "file:///log", Summary: "summary", MIMEType: "text/plain",
		SizeBytes: 42, Checksum: "sha256:abc", Metadata: map[string]string{"k": "v"},
	}); err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	if _, err := client.Artifacts().Get(t.Context(), "WS", "artifact-1"); err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	if got, err := client.Artifacts().List(t.Context(), "WS", store.ArtifactFilter{
		AgentID: "agent-1", SessionID: "sess-1", TerminalID: "term-1",
		TaskID: "task-1", Type: "log", Limit: 2,
	}); err != nil || len(got) != 1 {
		t.Fatalf("list artifacts = %+v err=%v", got, err)
	}
	agentID := "agent-2"
	sessionID := "sess-2"
	terminalID := "term-2"
	taskID := "task-2"
	artifactType := "transcript"
	uri := "file:///new"
	summary := "updated"
	mimeType := "text/plain"
	sizeBytes := int64(99)
	checksum := "sha256:def"
	meta := map[string]string{"k": "v"}
	if _, err := client.Artifacts().Update(t.Context(), "WS", "artifact-1", store.ArtifactUpdate{
		AgentID: &agentID, SessionID: &sessionID, TerminalID: &terminalID,
		TaskID: &taskID, Type: &artifactType, URI: &uri, Summary: &summary,
		MIMEType: &mimeType, SizeBytes: &sizeBytes, Checksum: &checksum, Metadata: &meta,
	}); err != nil {
		t.Fatalf("update artifact: %v", err)
	}
}

func TestControlPlaneClientLeasesOwnershipAndCommands(t *testing.T) {
	now := time.Now().UTC()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/agent-sessions/sess-1/leases":
			body := decodeBody(t, r)
			if body["lease_id"] != "lease-1" || body["agent_id"] != "agent-1" ||
				body["node_id"] != "node-1" || body["ttl_seconds"] != float64(30) {
				t.Fatalf("lease create body = %#v", body)
			}
			writeJSON(t, w, domain.AgentLease{WorkspaceKey: "WS", LeaseID: "lease-1", SessionID: "sess-1", Token: "token"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/agent-leases/lease-1":
			writeJSON(t, w, domain.AgentLease{WorkspaceKey: "WS", LeaseID: "lease-1", SessionID: "sess-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/agent-leases":
			q := r.URL.Query()
			if q.Get("session_id") != "sess-1" || q.Get("agent_id") != "agent-1" ||
				q.Get("node_id") != "node-1" || q.Get("status") != "active" || q.Get("limit") != "2" {
				t.Fatalf("lease list query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"agent_leases": []domain.AgentLease{{WorkspaceKey: "WS", LeaseID: "lease-1"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/agent-leases/lease-1/heartbeat":
			if r.Header.Get("X-Agent-Lease-Token") != "token" || r.URL.Query().Get("ttl_seconds") != "45" {
				t.Fatalf("lease heartbeat header/query = %q %s", r.Header.Get("X-Agent-Lease-Token"), r.URL.RawQuery)
			}
			writeJSON(t, w, domain.AgentLease{WorkspaceKey: "WS", LeaseID: "lease-1", LastHeartbeat: now})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/agent-leases/lease-1/release":
			if r.Header.Get("X-Agent-Lease-Token") != "token" {
				t.Fatalf("lease release token = %q", r.Header.Get("X-Agent-Lease-Token"))
			}
			writeJSON(t, w, domain.AgentLease{WorkspaceKey: "WS", LeaseID: "lease-1", Status: domain.AgentLeaseReleased})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/agent-ownership-leases/agent-1/acquire":
			body := decodeBody(t, r)
			if body["lease_id"] != "owner-lease" || body["owner_id"] != "owner" ||
				body["runtime_provider"] != "local" || body["node_id"] != "node-1" ||
				body["ttl_seconds"] != float64(60) {
				t.Fatalf("ownership acquire body = %#v", body)
			}
			writeJSON(t, w, domain.AgentOwnershipLease{WorkspaceKey: "WS", AgentID: "agent-1", Token: "owner-token"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/agent-ownership-leases/agent-1":
			writeJSON(t, w, domain.AgentOwnershipLease{WorkspaceKey: "WS", AgentID: "agent-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/agent-ownership-leases":
			q := r.URL.Query()
			if q.Get("owner_id") != "owner" || q.Get("node_id") != "node-1" ||
				q.Get("runtime_provider") != "local" || q.Get("status") != "active" ||
				q.Get("limit") != "2" {
				t.Fatalf("ownership list query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"agent_ownership_leases": []domain.AgentOwnershipLease{{WorkspaceKey: "WS", AgentID: "agent-1"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/agent-ownership-leases/agent-1/heartbeat":
			if r.Header.Get("X-Agent-Ownership-Lease-Token") != "owner-token" || r.URL.Query().Get("ttl_seconds") != "75" {
				t.Fatalf("ownership heartbeat header/query = %q %s", r.Header.Get("X-Agent-Ownership-Lease-Token"), r.URL.RawQuery)
			}
			writeJSON(t, w, domain.AgentOwnershipLease{WorkspaceKey: "WS", AgentID: "agent-1", LastHeartbeat: now})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/agent-ownership-leases/agent-1/release":
			if r.Header.Get("X-Agent-Ownership-Lease-Token") != "owner-token" {
				t.Fatalf("ownership release token = %q", r.Header.Get("X-Agent-Ownership-Lease-Token"))
			}
			writeJSON(t, w, domain.AgentOwnershipLease{WorkspaceKey: "WS", AgentID: "agent-1", Status: domain.AgentLeaseReleased})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/agent-commands/cmd-1":
			writeJSON(t, w, domain.AgentCommand{WorkspaceKey: "WS", CommandID: "cmd-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/agent-commands":
			q := r.URL.Query()
			if q.Get("target_agent_id") != "agent-1" || q.Get("target_node_id") != "node-1" ||
				q.Get("status") != "queued" || q.Get("after_cursor") != "5" ||
				q.Get("limit") != "2" {
				t.Fatalf("command list query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"agent_commands": []domain.AgentCommand{{WorkspaceKey: "WS", CommandID: "cmd-1"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/agent-commands/cmd-1/ack":
			writeJSON(t, w, domain.AgentCommand{WorkspaceKey: "WS", CommandID: "cmd-1", Status: domain.AgentCommandAcked})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/agent-commands/cmd-1/complete":
			body := decodeBody(t, r)
			if body["status"] != "failed" || body["result"] != "nope" || body["error_class"] != "fatal" {
				t.Fatalf("command complete body = %#v", body)
			}
			writeJSON(t, w, domain.AgentCommand{WorkspaceKey: "WS", CommandID: "cmd-1", Status: domain.AgentCommandFailed})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.AgentLeases().Create(t.Context(), store.AgentLeaseCreate{
		WorkspaceKey: "WS", SessionID: "sess-1", LeaseID: "lease-1",
		AgentID: "agent-1", NodeID: "node-1", TTL: 30 * time.Second,
	}); err != nil {
		t.Fatalf("create lease: %v", err)
	}
	if _, err := client.AgentLeases().Get(t.Context(), "WS", "lease-1"); err != nil {
		t.Fatalf("get lease: %v", err)
	}
	if got, err := client.AgentLeases().List(t.Context(), "WS", store.AgentLeaseFilter{
		SessionID: "sess-1", AgentID: "agent-1", NodeID: "node-1",
		Status: domain.AgentLeaseActive, Limit: 2,
	}); err != nil || len(got) != 1 {
		t.Fatalf("list leases = %+v err=%v", got, err)
	}
	if _, err := client.AgentLeases().Heartbeat(t.Context(), "WS", "lease-1", "token", 45*time.Second); err != nil {
		t.Fatalf("heartbeat lease: %v", err)
	}
	if _, err := client.AgentLeases().Release(t.Context(), "WS", "lease-1", "token"); err != nil {
		t.Fatalf("release lease: %v", err)
	}

	if _, err := client.AgentOwnershipLeases().Acquire(t.Context(), store.AgentOwnershipLeaseAcquire{
		WorkspaceKey: "WS", AgentID: "agent-1", LeaseID: "owner-lease",
		OwnerID: "owner", RuntimeProvider: domain.RuntimeProviderLocal,
		NodeID: "node-1", TTL: time.Minute,
	}); err != nil {
		t.Fatalf("acquire ownership lease: %v", err)
	}
	if _, err := client.AgentOwnershipLeases().Get(t.Context(), "WS", "agent-1"); err != nil {
		t.Fatalf("get ownership lease: %v", err)
	}
	if got, err := client.AgentOwnershipLeases().List(t.Context(), "WS", store.AgentOwnershipLeaseFilter{
		OwnerID: "owner", NodeID: "node-1", RuntimeProvider: domain.RuntimeProviderLocal,
		Status: domain.AgentLeaseActive, Limit: 2,
	}); err != nil || len(got) != 1 {
		t.Fatalf("list ownership leases = %+v err=%v", got, err)
	}
	if _, err := client.AgentOwnershipLeases().Heartbeat(t.Context(), "WS", "agent-1", "owner-token", 75*time.Second); err != nil {
		t.Fatalf("heartbeat ownership lease: %v", err)
	}
	if _, err := client.AgentOwnershipLeases().Release(t.Context(), "WS", "agent-1", "owner-token"); err != nil {
		t.Fatalf("release ownership lease: %v", err)
	}

	if _, err := client.AgentCommands().Get(t.Context(), "WS", "cmd-1"); err != nil {
		t.Fatalf("get command: %v", err)
	}
	if got, err := client.AgentCommands().List(t.Context(), "WS", store.AgentCommandFilter{
		TargetAgentID: "agent-1", TargetNodeID: "node-1",
		Status: domain.AgentCommandQueued, AfterCursor: 5, Limit: 2,
	}); err != nil || len(got) != 1 {
		t.Fatalf("list commands = %+v err=%v", got, err)
	}
	if _, err := client.AgentCommands().Ack(t.Context(), "WS", "cmd-1"); err != nil {
		t.Fatalf("ack command: %v", err)
	}
	if _, err := client.AgentCommands().Complete(t.Context(), "WS", "cmd-1", store.AgentCommandComplete{
		Status: domain.AgentCommandFailed, Result: "nope", ErrorClass: "fatal",
	}); err != nil {
		t.Fatalf("complete command: %v", err)
	}
}

func TestControlPlaneClientAgentSessionLifecycleAndNilLists(t *testing.T) {
	now := time.Now().UTC()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/agent-sessions":
			body := decodeBody(t, r)
			if body["session_id"] != "sess-1" || body["agent_id"] != "agent-1" ||
				body["node_id"] != "node-1" || body["kind"] != "task" ||
				body["terminal_id"] != "term-1" || body["parent_session_id"] != "orch-1" ||
				body["status"] != "running" || body["phase"] != "implementation" ||
				body["attempt"] != float64(3) || body["metadata"] == nil {
				t.Fatalf("agent session create body = %#v", body)
			}
			writeJSON(t, w, domain.AgentSession{WorkspaceKey: "WS", SessionID: "sess-1", AgentID: "agent-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/agent-sessions/sess-1":
			writeJSON(t, w, domain.AgentSession{WorkspaceKey: "WS", SessionID: "sess-1", AgentID: "agent-1"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/agent-sessions/sess-1/heartbeat":
			writeJSON(t, w, domain.AgentSession{WorkspaceKey: "WS", SessionID: "sess-1", LastHeartbeat: now})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/nodes":
			writeJSON(t, w, map[string]any{})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/agent-sessions":
			if r.URL.RawQuery != "" {
				t.Fatalf("agent sessions nil-list query = %q, want empty", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/terminal-sessions":
			writeJSON(t, w, map[string]any{})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/artifacts":
			writeJSON(t, w, map[string]any{})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/agent-leases":
			writeJSON(t, w, map[string]any{})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/agent-ownership-leases":
			writeJSON(t, w, map[string]any{})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/agent-commands":
			writeJSON(t, w, map[string]any{})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
		WorkspaceKey:    "WS",
		SessionID:       "sess-1",
		AgentID:         "agent-1",
		NodeID:          "node-1",
		Kind:            domain.AgentSessionKindTask,
		TaskID:          "task-1",
		TerminalID:      "term-1",
		ParentSessionID: "orch-1",
		Status:          domain.AgentSessionRunning,
		Phase:           "implementation",
		Attempt:         3,
		Metadata:        map[string]string{"k": "v"},
	}); err != nil {
		t.Fatalf("create agent session: %v", err)
	}
	if _, err := client.AgentSessions().Get(t.Context(), "WS", "sess-1"); err != nil {
		t.Fatalf("get agent session: %v", err)
	}
	if _, err := client.AgentSessions().Heartbeat(t.Context(), "WS", "sess-1"); err != nil {
		t.Fatalf("heartbeat agent session: %v", err)
	}

	if nodes, err := client.Nodes().List(t.Context(), "WS"); err != nil || len(nodes) != 0 {
		t.Fatalf("nil nodes list = %+v err=%v", nodes, err)
	}
	if sessions, err := client.AgentSessions().List(t.Context(), "WS", store.AgentSessionFilter{}); err != nil || len(sessions) != 0 {
		t.Fatalf("nil agent sessions list = %+v err=%v", sessions, err)
	}
	if terminals, err := client.TerminalSessions().List(t.Context(), "WS", store.TerminalSessionFilter{}); err != nil || len(terminals) != 0 {
		t.Fatalf("nil terminal sessions list = %+v err=%v", terminals, err)
	}
	if artifacts, err := client.Artifacts().List(t.Context(), "WS", store.ArtifactFilter{}); err != nil || len(artifacts) != 0 {
		t.Fatalf("nil artifacts list = %+v err=%v", artifacts, err)
	}
	if leases, err := client.AgentLeases().List(t.Context(), "WS", store.AgentLeaseFilter{}); err != nil || len(leases) != 0 {
		t.Fatalf("nil leases list = %+v err=%v", leases, err)
	}
	if ownership, err := client.AgentOwnershipLeases().List(t.Context(), "WS", store.AgentOwnershipLeaseFilter{}); err != nil || len(ownership) != 0 {
		t.Fatalf("nil ownership list = %+v err=%v", ownership, err)
	}
	if commands, err := client.AgentCommands().List(t.Context(), "WS", store.AgentCommandFilter{}); err != nil || len(commands) != 0 {
		t.Fatalf("nil commands list = %+v err=%v", commands, err)
	}
}

func TestControlPlaneClientStoreMethodErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	status := domain.AgentSessionRunning
	terminalStatus := domain.TerminalSessionClosed
	owner := "owner"

	cases := []struct {
		name string
		run  func() error
	}{
		{"node create", func() error {
			_, err := client.Nodes().Create(t.Context(), store.NodeCreate{WorkspaceKey: "WS", NodeID: "node-1"})
			return err
		}},
		{"node get", func() error {
			_, err := client.Nodes().Get(t.Context(), "WS", "node-1")
			return err
		}},
		{"node heartbeat", func() error {
			_, err := client.Nodes().Heartbeat(t.Context(), "WS", "node-1", 0)
			return err
		}},
		{"node update", func() error {
			_, err := client.Nodes().Update(t.Context(), "WS", "node-1", store.NodeUpdate{OwnerActor: &owner})
			return err
		}},
		{"agent session create", func() error {
			_, err := client.AgentSessions().Create(t.Context(), store.AgentSessionCreate{WorkspaceKey: "WS", SessionID: "sess-1", AgentID: "agent-1"})
			return err
		}},
		{"agent session get", func() error {
			_, err := client.AgentSessions().Get(t.Context(), "WS", "sess-1")
			return err
		}},
		{"agent session list", func() error {
			_, err := client.AgentSessions().List(t.Context(), "WS", store.AgentSessionFilter{})
			return err
		}},
		{"agent session heartbeat", func() error {
			_, err := client.AgentSessions().Heartbeat(t.Context(), "WS", "sess-1")
			return err
		}},
		{"agent session update", func() error {
			_, err := client.AgentSessions().Update(t.Context(), "WS", "sess-1", store.AgentSessionUpdate{Status: &status})
			return err
		}},
		{"terminal create", func() error {
			_, err := client.TerminalSessions().Create(t.Context(), store.TerminalSessionCreate{WorkspaceKey: "WS", TerminalID: "term-1"})
			return err
		}},
		{"terminal get", func() error {
			_, err := client.TerminalSessions().Get(t.Context(), "WS", "term-1")
			return err
		}},
		{"terminal update", func() error {
			_, err := client.TerminalSessions().Update(t.Context(), "WS", "term-1", store.TerminalSessionUpdate{Status: &terminalStatus})
			return err
		}},
		{"artifact create", func() error {
			_, err := client.Artifacts().Create(t.Context(), store.ArtifactCreate{WorkspaceKey: "WS", ArtifactID: "artifact-1"})
			return err
		}},
		{"artifact get", func() error {
			_, err := client.Artifacts().Get(t.Context(), "WS", "artifact-1")
			return err
		}},
		{"artifact update", func() error {
			_, err := client.Artifacts().Update(t.Context(), "WS", "artifact-1", store.ArtifactUpdate{AgentID: &owner})
			return err
		}},
		{"lease create", func() error {
			_, err := client.AgentLeases().Create(t.Context(), store.AgentLeaseCreate{WorkspaceKey: "WS", SessionID: "sess-1", LeaseID: "lease-1"})
			return err
		}},
		{"lease release", func() error {
			_, err := client.AgentLeases().Release(t.Context(), "WS", "lease-1", "token")
			return err
		}},
		{"ownership acquire", func() error {
			_, err := client.AgentOwnershipLeases().Acquire(t.Context(), store.AgentOwnershipLeaseAcquire{WorkspaceKey: "WS", AgentID: "agent-1"})
			return err
		}},
		{"ownership release", func() error {
			_, err := client.AgentOwnershipLeases().Release(t.Context(), "WS", "agent-1", "token")
			return err
		}},
		{"command ack", func() error {
			_, err := client.AgentCommands().Ack(t.Context(), "WS", "cmd-1")
			return err
		}},
		{"command complete", func() error {
			_, err := client.AgentCommands().Complete(t.Context(), "WS", "cmd-1", store.AgentCommandComplete{Status: domain.AgentCommandSucceeded})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
