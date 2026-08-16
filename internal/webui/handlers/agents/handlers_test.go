package agents

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

func TestHandleInteractivePromptsListsBuiltins(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/TEST2/interactive-prompts", nil)
	rr := httptest.NewRecorder()

	HandleInteractivePrompts().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rr.Code, rr.Body.String())
	}
	var got interactivePromptsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if bytes.Contains(rr.Body.Bytes(), []byte("hidden")) {
		t.Fatalf("interactive prompt wire response leaked hidden field: %s", rr.Body.String())
	}
	seen := map[string]string{}
	for _, prompt := range got.Prompts {
		seen[prompt.ID] = prompt.Label
	}
	if seen["lead"] != "Lead" || seen["pr-review"] != "PR Review" {
		t.Fatalf("prompts = %#v, want lead and pr-review", got.Prompts)
	}
	if _, ok := seen["pr-review-checkout"]; ok {
		t.Fatalf("hidden prompt pr-review-checkout was returned: %#v", got.Prompts)
	}
	if !agents.IsBuiltinInteractivePrompt("pr-review-checkout") {
		t.Fatal("pr-review-checkout must remain registered as a launchable builtin prompt")
	}
}

func TestBroadcastAgentRefreshEmitsGenericAgentEvent(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, realtime.ClientSendBuf, "0", nil, "ws-1")
	otherWorkspace := realtime.NewClient(2, realtime.ClientSendBuf, "0", nil, "ws-2")
	hub.RegisterClient(client)
	hub.RegisterClient(otherWorkspace)
	waitForAgentHubClients(t, hub, 2)

	broadcastAgentRefresh(hub, "ws-1", "agent-alpha", "tester")

	select {
	case got := <-client.Send():
		if got.Type != "refresh" || got.EntityType != "agent" || got.EntityID != "agent-alpha" ||
			got.Action != "agent.refresh" || got.Title != "agent-alpha" || got.Actor != "tester" ||
			got.WorkspaceID != "ws-1" || got.IssueID != "" {
			t.Fatalf("agent refresh = %+v", got)
		}
		if _, err := time.Parse(time.RFC3339Nano, got.Timestamp); err != nil {
			t.Fatalf("Timestamp = %q, want RFC3339Nano: %v", got.Timestamp, err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for agent refresh broadcast")
	}

	select {
	case got := <-otherWorkspace.Send():
		t.Fatalf("other workspace received agent refresh: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func waitForAgentHubClients(t *testing.T, hub *realtime.Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if hub.ClientCount() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("hub ClientCount() = %d, want %d", hub.ClientCount(), want)
}
