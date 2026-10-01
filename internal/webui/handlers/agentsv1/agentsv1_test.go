package agentsv1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func testAgent(id, state string) loomstore.Agent {
	return loomstore.Agent{AgentID: id, WorkspaceID: "ws", Name: id, ProfileKey: id, Preset: "lead",
		PresetVersion: "1", Mode: "persistent", InteractionMode: "interactive", RoleKind: "interactive",
		SpecJSON: "{}", SpecVersion: 1, OwnerKind: "user", OwnerID: "local", CreatedByKind: "user",
		CreatedByID: "local", CreateRequestID: "req-" + id, Repo: "/repo", Harness: "fake", State: state, Attempt: 1}
}

// newServer serves the Agent API for workspace "ws" with an idle agent a1
// and a busy agent b1 (a turn runs, so Sends wait). identity, when set, is
// the authenticated user the request carries.
func newServer(t *testing.T, identity *middleware.UserIdentity) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	st, err := loomstore.Open(ctx, filepath.Join(t.TempDir(), "loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	busy := testAgent("b1", loomagent.StateActive)
	turn := "turn_1"
	busy.RunningTurnID = &turn
	for _, a := range []loomstore.Agent{testAgent("a1", loomagent.StateIdle), busy} {
		if err := st.InsertAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	svc := loomagent.New(loomagent.ServiceConfig{Store: st, WorkspaceID: "ws"})
	mux := http.NewServeMux()
	ws := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := middleware.WithWorkspace(r.Context(), r.PathValue("ws"))
			if identity != nil {
				ctx = middleware.WithUserIdentity(ctx, *identity)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	New(func(id string) *loomagent.Service {
		if id == "ws" {
			return svc
		}
		return nil
	}, nil).Register(mux, ws)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func call(t *testing.T, srv *httptest.Server, method, path, key, body string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+"/api/workspaces/"+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s %s: %d %q", method, path, resp.StatusCode, raw)
		}
	}
	return resp.StatusCode, out
}

func want(t *testing.T, what string, status int, out map[string]any, wantStatus int, wantCode string) {
	t.Helper()
	if status != wantStatus || (wantCode != "" && out["code"] != wantCode) {
		t.Fatalf("%s = %d %v; want %d %s", what, status, out, wantStatus, wantCode)
	}
}

func waiting(t *testing.T, srv *httptest.Server, agent string) []any {
	t.Helper()
	status, out := call(t, srv, "GET", "ws/v1/agents/"+agent, "", "")
	want(t, "get", status, out, 200, "")
	w, _ := out["WaitingMessages"].([]any)
	return w
}

// TestAgentRESTRoutes calls every §9.1 REST method and checks its status,
// result and error code mapping.
func TestAgentRESTRoutes(t *testing.T) {
	srv := newServer(t, nil)

	status, out := call(t, srv, "GET", "ws/v1/agents", "", "")
	if agents, _ := out["agents"].([]any); status != 200 || len(agents) != 2 {
		t.Fatalf("list = %d %v", status, out)
	}
	status, out = call(t, srv, "GET", "ws/v1/agents?state=idle", "", "")
	if agents, _ := out["agents"].([]any); status != 200 || len(agents) != 1 {
		t.Fatalf("list idle = %d %v", status, out)
	}
	status, out = call(t, srv, "GET", "ws/v1/agents/a1", "", "")
	if status != 200 || out["AgentID"] != "a1" || out["HarnessSessionID"] != nil {
		t.Fatalf("get = %d %v", status, out)
	}
	status, out = call(t, srv, "GET", "ws/v1/agents/zz", "", "")
	want(t, "get unknown", status, out, 404, "agent_not_found")
	status, out = call(t, srv, "GET", "other/v1/agents", "", "")
	want(t, "workspace without Agent API", status, out, 404, "")

	status, out = call(t, srv, "PATCH", "ws/v1/agents/a1", "u1", `{"Name":"renamed","AgentID":"b1"}`)
	if status != 200 || out["Name"] != "renamed" || out["AgentID"] != "a1" {
		t.Fatalf("patch = %d %v", status, out)
	}

	status, out = call(t, srv, "POST", "ws/v1/agents/b1/messages", "s1", `{"Text":"hi"}`)
	if status != 202 || out["state"] != "waiting" || out["messageId"] == "" {
		t.Fatalf("send = %d %v", status, out)
	}
	first := out["messageId"]
	status, out = call(t, srv, "POST", "ws/v1/agents/b1/messages", "s1", `{"Text":"changed"}`)
	if status != 202 || out["messageId"] != first {
		t.Fatalf("send retry = %d %v; want the first result", status, out)
	}
	status, out = call(t, srv, "POST", "ws/v1/agents/b1/messages", "", `{"Text":"hi"}`)
	want(t, "send without Idempotency-Key", status, out, 400, "preset_invalid")
	status, out = call(t, srv, "POST", "ws/v1/agents/b1/messages", "s2", `{"Text":`)
	want(t, "send bad JSON", status, out, 400, "")

	status, out = call(t, srv, "DELETE", "ws/v1/agents/b1/messages/waiting", "w1", "")
	if status != 200 || out["Result"] != "withdrawn" {
		t.Fatalf("withdraw = %d %v", status, out)
	}
	status, out = call(t, srv, "DELETE", "ws/v1/agents/b1/messages/waiting", "w2", "")
	if status != 200 || out["Result"] != "nothing_waiting" {
		t.Fatalf("withdraw again = %d %v", status, out)
	}

	status, out = call(t, srv, "POST", "ws/v1/agents/a1/asks/ask_1", "r1", `{"Decision":"allow_once"}`)
	want(t, "respond", status, out, 404, "ask_not_found")

	status, out = call(t, srv, "GET", "ws/v1/agents/a1/events?after=0&limit=10&kind=agent.updated", "", "")
	if evs, _ := out["Events"].([]any); status != 200 || len(evs) != 1 {
		t.Fatalf("events = %d %v", status, out)
	}
	status, out = call(t, srv, "GET", "ws/v1/agents/a1/events?after=x", "", "")
	want(t, "events bad cursor", status, out, 400, "")

	status, out = call(t, srv, "POST", "ws/v1/agents/a1/archive", "ar1", `{"Reason":"cancelled"}`)
	want(t, "archive", status, out, 204, "")
	status, out = call(t, srv, "POST", "ws/v1/agents/a1/messages", "s3", `{"Text":"hi"}`)
	want(t, "send to archived", status, out, 409, "agent_archived")
	status, out = call(t, srv, "POST", "ws/v1/agents/a1/unarchive", "ua1", "")
	want(t, "unarchive", status, out, 204, "")
	status, out = call(t, srv, "DELETE", "ws/v1/agents/zz?cascade=true", "d1", "")
	want(t, "delete unknown", status, out, 404, "agent_not_found")

	status, out = call(t, srv, "POST", "ws/v1/agents", "", `{"Preset":"lead"}`)
	want(t, "create without Idempotency-Key", status, out, 400, "preset_invalid")
	status, out = call(t, srv, "POST", "ws/v1/agents", "c1", `{"Preset":"nope"}`)
	want(t, "create unknown preset", status, out, 404, "preset_not_found")

	status, out = call(t, srv, "GET", "ws/v1/presets", "", "")
	if ps, _ := out["presets"].([]any); status != 200 || len(ps) != 5 {
		t.Fatalf("presets = %d %v", status, out)
	}
	status, out = call(t, srv, "GET", "ws/v1/presets/lead", "", "")
	if status != 200 || out["Name"] != "lead" {
		t.Fatalf("preset = %d %v", status, out)
	}
	status, out = call(t, srv, "GET", "ws/v1/presets/nope", "", "")
	want(t, "unknown preset", status, out, 404, "preset_not_found")
}

// TestAgentSenderFromAuthNotBody: the sender is the authenticated caller;
// an Actor, Source or RequestID in the body is ignored.
func TestAgentSenderFromAuthNotBody(t *testing.T) {
	body := `{"Text":"hi","Actor":{"Kind":"agent","ID":"evil"},"Source":"system","RequestID":"body-id"}`
	for _, tc := range []struct {
		identity *middleware.UserIdentity
		sender   string
	}{
		{nil, "user:local"},
		{&middleware.UserIdentity{UserID: "sub-1", Email: "a@example.com"}, "user:sub-1"},
	} {
		srv := newServer(t, tc.identity)
		status, out := call(t, srv, "POST", "ws/v1/agents/b1/messages", "k1", body)
		want(t, "send", status, out, 202, "")
		w := waiting(t, srv, "b1")
		if len(w) != 1 || w[0].(map[string]any)["Sender"] != tc.sender {
			t.Fatalf("waiting = %v; want one from %s", w, tc.sender)
		}
		// The body's RequestID is not the key: a retry under it is a new Send.
		status, out = call(t, srv, "POST", "ws/v1/agents/b1/messages", "body-id", `{"Text":"edit"}`)
		if status != 202 || out["replaced"] != true {
			t.Fatalf("send under the body's RequestID = %d %v; want a new Send that replaces", status, out)
		}
	}
}

// TestAgentSendRESTBodyLimit: Send is bounded only by the 1 MiB JSON body.
// A body just under the limit, with multi-byte UTF-8 and JSON escapes, and a
// ~100 KB message round-trip byte for byte; a body over it fails with 413
// and stores nothing.
func TestAgentSendRESTBodyLimit(t *testing.T) {
	srv := newServer(t, nil)
	unit := "résumé ✓ 日本語 \"quoted\"\n\t<tag>&\\ 🚀 "
	encode := func(text string) string {
		b, _ := json.Marshal(map[string]string{"Text": text})
		return string(b)
	}
	per := len(encode(unit+unit)) - len(encode(unit))
	near := strings.Repeat(unit, (handler.MaxRequestBody-len(encode("")))/per)
	if n := len(encode(near)); n > handler.MaxRequestBody || n < handler.MaxRequestBody-per || !utf8.ValidString(near) {
		t.Fatalf("fixture body %d bytes", n)
	}
	over := near + strings.Repeat(unit, 2)
	if len(encode(over)) <= handler.MaxRequestBody {
		t.Fatal("fixture: over-limit body is not over the limit")
	}
	status, out := call(t, srv, "POST", "ws/v1/agents/b1/messages", "big", encode(over))
	want(t, "send over 1 MiB", status, out, 413, "")
	if w := waiting(t, srv, "b1"); len(w) != 0 {
		t.Fatalf("over-limit Send stored %d messages", len(w))
	}
	for i, text := range []string{strings.Repeat(unit, 100_000/len(unit)), near} {
		status, out = call(t, srv, "POST", "ws/v1/agents/b1/messages", "k"+string(rune('0'+i)), encode(text))
		want(t, "send", status, out, 202, "")
		w := waiting(t, srv, "b1")
		if len(w) != 1 || w[0].(map[string]any)["Text"] != text {
			t.Fatalf("send %d bytes: stored text differs", len(text))
		}
	}
}
