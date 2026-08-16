package fleetdb

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
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
			writeJSON(t, w, map[string]any{
				"workspace_key": "WS", "node_id": req.NodeID, "runtime_provider": "local",
				"drain_state": "active", "last_heartbeat": now, "expires_at": now.Add(30 * time.Second),
				"created_at": now, "updated_at": now,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/nodes/node-1/heartbeat":
			if r.URL.Query().Get("ttl_seconds") != "45" {
				t.Fatalf("ttl_seconds = %q", r.URL.Query().Get("ttl_seconds"))
			}
			writeJSON(t, w, map[string]any{
				"workspace_key": "WS", "node_id": "node-1", "runtime_provider": "local",
				"drain_state": "active", "last_heartbeat": now, "expires_at": now.Add(45 * time.Second),
				"created_at": now, "updated_at": now,
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	node, err := client.Nodes().Create(t.Context(), execution.NodeCreate{WorkspaceKey: "WS", NodeID: "node-1", TTL: 30 * time.Second})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	if !sawCreate || node.WorkspaceKey != "WS" || node.NodeID != "node-1" ||
		node.RuntimeProvider != execution.RuntimeProviderLocal || node.DrainState != execution.WorkerNodeActive {
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
		writeJSON(t, w, map[string]any{"agent_sessions": []interaction.SessionRecord{{WorkspaceKey: "WS", SessionID: "sess-1", AgentID: "agent-1"}}})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := client.AgentSessions().List(t.Context(), "WS", interaction.AgentSessionFilter{
		AgentID: "agent-1",
		NodeID:  "node-1",
		TaskID:  "T-1",
		Status:  interaction.SessionRecordRunning,
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
		writeJSON(t, w, map[string]any{"agent_sessions": []interaction.SessionRecord{
			{WorkspaceKey: "WS", SessionID: "orch-1", Kind: interaction.SessionRecordInteractive},
			{WorkspaceKey: "WS", SessionID: "task-a", Kind: interaction.SessionRecordTask, ParentSessionID: "orch-1"},
			{WorkspaceKey: "WS", SessionID: "task-b", Kind: interaction.SessionRecordTask, ParentSessionID: "orch-1"},
			{WorkspaceKey: "WS", SessionID: "task-c", Kind: interaction.SessionRecordTask, ParentSessionID: "orch-other"},
		}})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.AgentSessions().List(t.Context(), "WS", interaction.AgentSessionFilter{
		Kind:            interaction.SessionRecordTask,
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
		if s.Kind != interaction.SessionRecordTask || s.ParentSessionID != "orch-1" {
			t.Fatalf("unexpected session passed filter: %+v", s)
		}
	}
}

func TestControlPlaneClientAgentSessionUpdateBodyUsesWireNames(t *testing.T) {
	finishedAt := time.Now().UTC()
	exitCode := 7
	finishedAtPtr := &finishedAt
	exitCodePtr := &exitCode
	status := interaction.SessionRecordFailed
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
		writeJSON(t, w, interaction.SessionRecord{
			WorkspaceKey: "WS",
			SessionID:    "sess-1",
			AgentID:      "agent-1",
			TaskID:       "T-1",
			Status:       interaction.SessionRecordFailed,
			ExitCode:     &exitCode,
			FinishedAt:   &finishedAt,
		})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.AgentSessions().Update(t.Context(), "WS", "sess-1", interaction.AgentSessionUpdate{
		TaskID:     &taskID,
		Status:     &status,
		FinishedAt: &finishedAtPtr,
		ErrorClass: &errClass,
		ExitCode:   &exitCodePtr,
	})
	if err != nil {
		t.Fatalf("update session: %v", err)
	}
	if session.TaskID != "T-1" || session.Status != interaction.SessionRecordFailed {
		t.Fatalf("session = %+v", session)
	}
}

func TestControlPlaneClientAgentLeaseCreateDecodesOneTimeTokenEnvelope(t *testing.T) {
	now := time.Now().UTC()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/agent-sessions/session-1/leases" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode create: %v", err)
		}
		if body["lease_id"] != "lease-1" || body["agent_id"] != "agent-1" || body["node_id"] != "node-1" || body["ttl_seconds"] != float64(30) {
			t.Fatalf("create body = %#v", body)
		}
		writeJSON(t, w, map[string]any{
			"lease": interaction.LeaseRecord{
				WorkspaceKey:  "WS",
				LeaseID:       "lease-1",
				SessionID:     "session-1",
				AgentID:       "agent-1",
				NodeID:        "node-1",
				FencingToken:  7,
				Status:        interaction.LeaseRecordActive,
				ExpiresAt:     now.Add(30 * time.Second),
				LastHeartbeat: now,
				CreatedAt:     now,
				UpdatedAt:     now,
			},
			"token": "one-time-session-token",
		})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := client.AgentLeases().Create(t.Context(), interaction.AgentLeaseCreate{
		WorkspaceKey: "WS",
		SessionID:    "session-1",
		LeaseID:      "lease-1",
		AgentID:      "agent-1",
		NodeID:       "node-1",
		TTL:          30 * time.Second,
	})
	if err != nil {
		t.Fatalf("create lease: %v", err)
	}
	if lease.Token != "one-time-session-token" || lease.FencingToken != 7 {
		t.Fatalf("lease = %+v", lease)
	}
}

func TestControlPlaneClientAgentOwnershipLeaseAcquireDecodesOneTimeTokenEnvelope(t *testing.T) {
	now := time.Now().UTC()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/agent-ownership-leases/agent-1/acquire" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		if got := r.Header.Get(FleetDelegatedActorHeader); got != "runtime-1" {
			t.Fatalf("%s = %q, want runtime-1", FleetDelegatedActorHeader, got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode acquire: %v", err)
		}
		if body["lease_id"] != "ownership-1" || body["owner_id"] != "runtime-1" ||
			body["runtime_provider"] != "local" || body["node_id"] != "node-1" ||
			body["ttl_seconds"] != float64(45) {
			t.Fatalf("acquire body = %#v", body)
		}
		writeJSON(t, w, map[string]any{
			"lease": agents.OwnershipRecord{
				WorkspaceKey:    "WS",
				AgentID:         "agent-1",
				LeaseID:         "ownership-1",
				OwnerID:         "runtime-1",
				RuntimeProvider: agents.RuntimeProviderLocal,
				NodeID:          "node-1",
				FencingToken:    11,
				Status:          agents.OwnershipActive,
				ExpiresAt:       now.Add(45 * time.Second),
				LastHeartbeat:   now,
				CreatedAt:       now,
				UpdatedAt:       now,
			},
			"token": "one-time-ownership-token",
		})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := client.AgentOwnershipLeases().Acquire(t.Context(), agents.AgentOwnershipLeaseAcquire{
		WorkspaceKey:    "WS",
		AgentID:         "agent-1",
		LeaseID:         "ownership-1",
		OwnerID:         "runtime-1",
		RuntimeProvider: agents.RuntimeProviderLocal,
		NodeID:          "node-1",
		TTL:             45 * time.Second,
	})
	if err != nil {
		t.Fatalf("acquire ownership lease: %v", err)
	}
	if lease.Token != "one-time-ownership-token" || lease.FencingToken != 11 {
		t.Fatalf("lease = %+v", lease)
	}
}

func TestValidateAgentOwnershipLeaseEnvelopeAcceptsOnlyNonEmptyServerGeneratedLeaseID(t *testing.T) {
	in := agents.AgentOwnershipLeaseAcquire{
		WorkspaceKey: "WS", AgentID: "agent-1", OwnerID: "runtime-1",
		RuntimeProvider: agents.RuntimeProviderLocal, NodeID: "node-1",
	}
	lease := agents.OwnershipRecord{
		WorkspaceKey: in.WorkspaceKey, AgentID: in.AgentID, LeaseID: "ol-generated",
		OwnerID: in.OwnerID, RuntimeProvider: in.RuntimeProvider, NodeID: in.NodeID,
		FencingToken: 1,
	}
	if err := validateAgentOwnershipLeaseEnvelope(lease, "one-time-token", in); err != nil {
		t.Fatalf("server-generated lease id rejected: %v", err)
	}
	lease.LeaseID = ""
	if err := validateAgentOwnershipLeaseEnvelope(lease, "one-time-token", in); err == nil ||
		!strings.Contains(err.Error(), "omitted lease id") {
		t.Fatalf("empty server-generated lease id error = %v", err)
	}
	lease.LeaseID = "different"
	in.LeaseID = "requested"
	if err := validateAgentOwnershipLeaseEnvelope(lease, "one-time-token", in); err == nil ||
		!strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched requested lease id error = %v", err)
	}
}

func TestControlPlaneClientOwnedAgentOwnershipLifecycleSendsCompleteProof(t *testing.T) {
	now := time.Now().UTC()
	proof := agents.AgentOwnershipLeaseProof{
		WorkspaceKey: "WS", AgentID: "agent-1", LeaseID: "ownership-1",
		LeaseToken: "raw-ownership-token", OwnerID: "runtime-1",
		RuntimeProvider: agents.RuntimeProviderLocal, NodeID: "node-1", FencingToken: 11,
	}
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get(FleetDelegatedActorHeader); got != proof.OwnerID {
			t.Fatalf("%s = %q, want %q", FleetDelegatedActorHeader, got, proof.OwnerID)
		}
		if got := r.Header.Get(AgentOwnershipLeaseTokenHeader); got != proof.LeaseToken {
			t.Fatalf("%s = %q, want exact proof token", AgentOwnershipLeaseTokenHeader, got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode ownership command: %v", err)
		}
		if body["lease_id"] != proof.LeaseID || body["owner_id"] != proof.OwnerID ||
			body["runtime_provider"] != "local" || body["node_id"] != proof.NodeID ||
			body["fencing_token"] != float64(proof.FencingToken) {
			t.Fatalf("ownership proof body = %#v", body)
		}
		switch r.URL.Path {
		case "/api/v1/WS/agent-ownership-leases/agent-1/heartbeat":
			if body["ttl_seconds"] != float64(45) {
				t.Fatalf("heartbeat ttl_seconds = %#v, want 45", body["ttl_seconds"])
			}
		case "/api/v1/WS/agent-ownership-leases/agent-1/release":
			if _, ok := body["ttl_seconds"]; ok {
				t.Fatalf("release body must not contain ttl_seconds: %#v", body)
			}
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		writeJSON(t, w, agents.OwnershipRecord{
			WorkspaceKey: proof.WorkspaceKey, AgentID: proof.AgentID, LeaseID: proof.LeaseID,
			OwnerID: proof.OwnerID, RuntimeProvider: proof.RuntimeProvider, NodeID: proof.NodeID,
			FencingToken: proof.FencingToken, Status: agents.OwnershipActive,
			LastHeartbeat: now, ExpiresAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now,
		})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "loom-local-service"})
	if err != nil {
		t.Fatal(err)
	}
	owned, ok := client.AgentOwnershipLeases().(agents.AgentOwnershipLeaseOwnedStore)
	if !ok {
		t.Fatal("FleetDB ownership adapter does not expose owner-fenced lifecycle commands")
	}
	if _, err := owned.HeartbeatOwned(t.Context(), proof, 45*time.Second); err != nil {
		t.Fatalf("heartbeat owned: %v", err)
	}
	if _, err := owned.ReleaseOwned(t.Context(), proof); err != nil {
		t.Fatalf("release owned: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestControlPlaneClientLeaseCreationRejectsMissingEnvelopeToken(t *testing.T) {
	tests := []struct {
		name string
		path string
		run  func(*Client) error
		body any
	}{
		{
			name: "session lease",
			path: "/api/v1/WS/agent-sessions/session-1/leases",
			run: func(client *Client) error {
				_, err := client.AgentLeases().Create(t.Context(), interaction.AgentLeaseCreate{
					WorkspaceKey: "WS",
					SessionID:    "session-1",
					LeaseID:      "lease-1",
				})
				return err
			},
			body: interaction.LeaseRecord{
				WorkspaceKey: "WS",
				LeaseID:      "lease-1",
				SessionID:    "session-1",
				FencingToken: 1,
			},
		},
		{
			name: "ownership lease",
			path: "/api/v1/WS/agent-ownership-leases/agent-1/acquire",
			run: func(client *Client) error {
				_, err := client.AgentOwnershipLeases().Acquire(t.Context(), agents.AgentOwnershipLeaseAcquire{
					WorkspaceKey:    "WS",
					AgentID:         "agent-1",
					LeaseID:         "ownership-1",
					OwnerID:         "runtime-1",
					RuntimeProvider: agents.RuntimeProviderLocal,
					NodeID:          "node-1",
				})
				return err
			},
			body: map[string]any{"lease": agents.OwnershipRecord{
				WorkspaceKey: "WS", AgentID: "agent-1", LeaseID: "ownership-1",
				OwnerID: "runtime-1", RuntimeProvider: agents.RuntimeProviderLocal,
				NodeID: "node-1", FencingToken: 1,
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != test.path {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
				}
				writeJSON(t, w, test.body)
			}))
			defer ts.Close()
			client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			if err := test.run(client); err == nil || !strings.Contains(err.Error(), "omitted one-time token") {
				t.Fatalf("error = %v, want missing token failure", err)
			}
		})
	}
}

func TestControlPlaneClientArtifactReadContent(t *testing.T) {
	body := []byte("transcript bytes")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/WS/artifacts/transcript-1/content" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		if got := r.Header.Get("Accept"); got != "*/*" {
			t.Fatalf("accept = %q, want */*", got)
		}
		if got := r.Header.Get("X-Actor"); got != "tester" {
			t.Fatalf("actor = %q, want tester", got)
		}
		w.Header().Set("Content-Type", "application/jsonl")
		_, _ = w.Write(body)
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.ArtifactQueries().ReadArtifactContent(t.Context(), "WS", "transcript-1")
	if err != nil {
		t.Fatalf("read content: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("body = %q, want %q", got, body)
	}
}

func TestControlPlaneClientArtifactReadContentAboveGenericResponseLimit(t *testing.T) {
	const bodySize = maxResponseBody + (1 << 20)
	chunk := bytes.Repeat([]byte("x"), 32<<10)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/WS/artifacts/large-patch/content" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "text/x-diff")
		remaining := bodySize
		for remaining > 0 {
			n := min(remaining, len(chunk))
			if _, err := w.Write(chunk[:n]); err != nil {
				return
			}
			remaining -= n
		}
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.ArtifactQueries().ReadArtifactContent(t.Context(), "WS", "large-patch")
	if err != nil {
		t.Fatalf("read content above generic limit: %v", err)
	}
	if len(got) != bodySize {
		t.Fatalf("body length = %d, want %d", len(got), bodySize)
	}
	if got[0] != 'x' || got[len(got)-1] != 'x' {
		t.Fatal("artifact content was corrupted")
	}
}

func TestControlPlaneClientArtifactReadContentPreservesTypedFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		wantTarget error
	}{
		{
			name:       "managed content missing",
			status:     http.StatusNotFound,
			body:       `{"error":{"code":"not_found","message":"content not found"}}`,
			wantTarget: artifacts.ErrNotFound,
		},
		{
			name:       "content store unavailable",
			status:     http.StatusServiceUnavailable,
			body:       `{"error":{"code":"internal_error","message":"content store unavailable"}}`,
			wantTarget: artifacts.ErrContentUnavailable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer ts.Close()
			client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.ArtifactQueries().ReadArtifactContent(t.Context(), "WS", "transcript-1")
			if !errors.Is(err, tc.wantTarget) {
				t.Fatalf("ReadContent() error = %v, want errors.Is(%v)", err, tc.wantTarget)
			}
		})
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
