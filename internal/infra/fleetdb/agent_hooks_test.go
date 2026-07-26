package fleetdb

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func hookPipeline() *domain.AgentHooks {
	return &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
		{Type: domain.AgentHookActionAddLabel, Value: "criticized"},
	}}
}

// hookWireServer captures the raw request body so a test can assert the exact
// JSON that reaches fleet-db, not just the decoded struct.
type hookWireServer struct {
	*httptest.Server
	method string
	path   string
	body   string
}

func newHookWireServer(t *testing.T, respond func() any) *hookWireServer {
	t.Helper()
	hs := &hookWireServer{}
	hs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		hs.method, hs.path, hs.body = r.Method, r.URL.Path, string(raw)
		writeJSON(t, w, respond())
	}))
	t.Cleanup(hs.Close)
	return hs
}

func newHookClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c, err := New(Config{BaseURL: baseURL, Actor: "tester"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

func TestAgentStore_CreateSendsHooks(t *testing.T) {
	hs := newHookWireServer(t, func() any {
		return domain.Agent{WorkspaceKey: "WS", Name: "critic", RoleName: "critic", Hooks: hookPipeline()}
	})
	c := newHookClient(t, hs.URL)

	got, err := c.Agents().Create(context.Background(), store.AgentCreate{
		WorkspaceKey: "WS", Name: "critic", RoleName: "critic", Hooks: hookPipeline(),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(hs.body), &body); err != nil {
		t.Fatalf("decode request body %q: %v", hs.body, err)
	}
	raw, ok := body["hooks"]
	if !ok {
		t.Fatalf("create body is missing hooks: %s", hs.body)
	}
	var sent domain.AgentHooks
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatalf("decode hooks %s: %v", raw, err)
	}
	if !sent.Equal(hookPipeline()) {
		t.Errorf("sent hooks = %+v, want the configured pipeline", sent.OnComplete)
	}
	if !strings.Contains(hs.body, `"on_complete"`) {
		t.Errorf("wire body should use the on_complete key: %s", hs.body)
	}
	if !got.Hooks.Equal(hookPipeline()) {
		t.Errorf("decoded response hooks = %+v, want the pipeline", got.Hooks)
	}
}

func TestAgentStore_CreateOmitsHooksWhenUnset(t *testing.T) {
	hs := newHookWireServer(t, func() any {
		return domain.Agent{WorkspaceKey: "WS", Name: "plain", RoleName: "critic"}
	})
	c := newHookClient(t, hs.URL)

	got, err := c.Agents().Create(context.Background(), store.AgentCreate{
		WorkspaceKey: "WS", Name: "plain", RoleName: "critic",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if strings.Contains(hs.body, "hooks") {
		t.Errorf("hookless create must not send a hooks key: %s", hs.body)
	}
	if got.Hooks != nil {
		t.Errorf("Hooks = %+v, want nil", got.Hooks)
	}
}

func TestAgentStore_UpdateSendsHooksAndClear(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		hs := newHookWireServer(t, func() any {
			return domain.Agent{WorkspaceKey: "WS", Name: "critic", Hooks: hookPipeline()}
		})
		c := newHookClient(t, hs.URL)

		if _, err := c.Agents().Update(context.Background(), "WS", "critic",
			store.AgentUpdate{Hooks: hookPipeline()}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if hs.method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH (a hooks-only patch must not short-circuit to GET)", hs.method)
		}
		if !strings.Contains(hs.body, `"on_complete"`) {
			t.Errorf("patch body is missing the pipeline: %s", hs.body)
		}
	})

	t.Run("clear sends an empty object", func(t *testing.T) {
		hs := newHookWireServer(t, func() any {
			return domain.Agent{WorkspaceKey: "WS", Name: "critic"}
		})
		c := newHookClient(t, hs.URL)

		got, err := c.Agents().Update(context.Background(), "WS", "critic",
			store.AgentUpdate{Hooks: &domain.AgentHooks{}})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if hs.method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", hs.method)
		}
		// omitempty drops only a nil pointer, so the clear marker must survive.
		var body map[string]json.RawMessage
		if err := json.Unmarshal([]byte(hs.body), &body); err != nil {
			t.Fatalf("decode body %q: %v", hs.body, err)
		}
		raw, ok := body["hooks"]
		if !ok {
			t.Fatalf("clear patch dropped the hooks key: %s", hs.body)
		}
		if string(raw) != "{}" {
			t.Errorf("clear marker = %s, want {}", raw)
		}
		if got.Hooks != nil {
			t.Errorf("Hooks = %+v, want nil after a clear", got.Hooks)
		}
	})

	t.Run("an empty patch still short-circuits to GET", func(t *testing.T) {
		hs := newHookWireServer(t, func() any {
			return domain.Agent{WorkspaceKey: "WS", Name: "critic"}
		})
		c := newHookClient(t, hs.URL)

		if _, err := c.Agents().Update(context.Background(), "WS", "critic",
			store.AgentUpdate{}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if hs.method != http.MethodGet {
			t.Errorf("method = %s, want GET for a patch with no fields", hs.method)
		}
	})
}

func TestAgentUpdateHasFleetDBFields_Hooks(t *testing.T) {
	if agentUpdateHasFleetDBFields(store.AgentUpdate{}) {
		t.Error("an empty patch should have no fleet-db fields")
	}
	if !agentUpdateHasFleetDBFields(store.AgentUpdate{Hooks: &domain.AgentHooks{}}) {
		t.Error("a hooks-only clear patch must count as a fleet-db field")
	}
	if !agentUpdateHasFleetDBFields(store.AgentUpdate{Hooks: hookPipeline()}) {
		t.Error("a hooks-only set patch must count as a fleet-db field")
	}
}

func TestAgentWire_ToDomainClonesHooks(t *testing.T) {
	shared := hookPipeline()
	wire := agentWire{Name: "critic", Hooks: shared}

	got := wire.toDomain()
	if !got.Hooks.Equal(shared) {
		t.Fatal("toDomain should carry the pipeline")
	}
	got.Hooks.OnComplete[1].Value = "mutated"
	if shared.OnComplete[1].Value != "criticized" {
		t.Error("toDomain aliased the wire slice instead of cloning it")
	}
}
