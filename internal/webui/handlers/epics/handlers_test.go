package epics

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

func newMux(st store.Store, hub *realtime.Hub) *http.ServeMux {
	mux := http.NewServeMux()
	NewModule(st, hub).Register(mux)
	return mux
}

func postRun(t *testing.T, mux *http.ServeMux, ws, epicID string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+ws+"/epics/"+epicID+"/run", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Actor", "tester")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	var got map[string]any
	if rr.Body.Len() > 0 {
		_ = json.Unmarshal(rr.Body.Bytes(), &got)
	}
	return rr, got
}

func TestRunEpicBindsLead(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createLead(t, st, "WS", "nova", "", "session-1")

	rr, body := postRun(t, newMux(st, nil), "WS", "EPIC-1", map[string]string{"lead": "nova"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rr.Code, body)
	}
	if body["state"] != string("assigned") || body["delivery_state"] != "pending" || body["orchestrator_session_id"] != "session-1" {
		t.Fatalf("body = %#v, want assigned pending session-1", body)
	}
	got, err := st.Agents().Get(ctx, "WS", "nova")
	if err != nil {
		t.Fatalf("get lead: %v", err)
	}
	if got.Parent != "EPIC-1" {
		t.Fatalf("lead parent = %q, want EPIC-1", got.Parent)
	}
}

func TestRunEpicBroadcastsAgentRefresh(t *testing.T) {
	st := newTestStore(t)
	createLead(t, st, "WS", "nova", "", "")
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	client := realtime.NewClient(1, 8, "", nil, "WS")
	hub.RegisterClient(client)
	waitForHubClient(t, hub)

	rr, body := postRun(t, newMux(st, hub), "WS", "EPIC-1", map[string]string{"lead": "nova"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rr.Code, body)
	}
	expectAgentRefresh(t, client.Send(), "WS", "nova")
}

func TestRunEpicRejectsLeadAlreadyOnDifferentEpic(t *testing.T) {
	st := newTestStore(t)
	createLead(t, st, "WS", "nova", "EPIC-X", "")

	rr, body := postRun(t, newMux(st, nil), "WS", "EPIC-1", map[string]string{"lead": "nova"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%v", rr.Code, body)
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "already running epic EPIC-X") {
		t.Fatalf("error = %q, want it to mention the existing epic", msg)
	}
}

func TestRunEpicRejectsEpicAlreadyClaimedByOtherLead(t *testing.T) {
	st := newTestStore(t)
	createLead(t, st, "WS", "atlas", "EPIC-1", "")
	createLead(t, st, "WS", "nova", "", "")

	rr, body := postRun(t, newMux(st, nil), "WS", "EPIC-1", map[string]string{"lead": "nova"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%v", rr.Code, body)
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "already claimed by lead atlas") {
		t.Fatalf("error = %q, want claim conflict mentioning atlas", msg)
	}
}

func TestRunEpicIdempotentWhenAlreadyBound(t *testing.T) {
	st := newTestStore(t)
	createLead(t, st, "WS", "nova", "EPIC-1", "")

	rr, body := postRun(t, newMux(st, nil), "WS", "EPIC-1", map[string]string{"lead": "nova"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rr.Code, body)
	}
	if body["state"] != "resumed" || body["delivery_state"] != "delivered" {
		t.Fatalf("body = %#v, want resumed/delivered", body)
	}
}

func TestRunEpicRejectsNonLeadAgent(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.Agents().Create(context.Background(), store.AgentCreate{
		WorkspaceKey: "WS",
		Name:         "worker",
		RoleName:     "task",
	}); err != nil {
		t.Fatalf("create task agent: %v", err)
	}

	rr, body := postRun(t, newMux(st, nil), "WS", "EPIC-1", map[string]string{"lead": "worker"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%v", rr.Code, body)
	}
}

func TestRunEpicRejectsMissingLead(t *testing.T) {
	st := newTestStore(t)

	rr, body := postRun(t, newMux(st, nil), "WS", "EPIC-1", map[string]string{"lead": "ghost"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%v", rr.Code, body)
	}
}

func TestRunEpicRejectsBlankBody(t *testing.T) {
	st := newTestStore(t)

	rr, body := postRun(t, newMux(st, nil), "WS", "EPIC-1", map[string]string{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%v", rr.Code, body)
	}
}

func newTestStore(t *testing.T) store.Store {
	t.Helper()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	return memstore.New()
}

func createLead(t *testing.T, st store.Store, workspace, name, parent, orchestrator string) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: workspace,
		Name:         name,
		RoleName:     "lead",
		Parent:       parent,
	}); err != nil {
		t.Fatalf("create lead: %v", err)
	}
	if orchestrator == "" {
		return
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: workspace,
		SessionID:    orchestrator,
		AgentID:      name,
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create orchestrator session: %v", err)
	}
}

func waitForHubClient(t *testing.T, hub *realtime.Hub) {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if hub.ClientCount() == 1 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("SSE client was not registered")
		case <-ticker.C:
		}
	}
}

func expectAgentRefresh(t *testing.T, events <-chan *realtime.MutationPayload, workspace, agentName string) {
	t.Helper()
	select {
	case event := <-events:
		if event == nil {
			t.Fatal("SSE event channel closed")
		}
		if event.Type != "refresh" || event.WorkspaceID != workspace || event.EntityType != "agent" || event.EntityID != agentName {
			t.Fatalf("unexpected refresh event: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for agent refresh event for %s", agentName)
	}
}
