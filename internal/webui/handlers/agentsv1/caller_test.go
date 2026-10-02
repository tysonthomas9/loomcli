package agentsv1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// newBridgeServer serves workspaces "ws" and "ws2" with leads a1 and b1 in
// ws, each with one busy child (c1 under a1, c2 under b1), and lead x1 in ws2.
func newBridgeServer(t *testing.T) (*httptest.Server, *Tokens) {
	t.Helper()
	ctx := context.Background()
	st, err := loomstore.Open(ctx, filepath.Join(t.TempDir(), "loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	child := func(id, parent string) loomstore.Agent {
		a := testAgent(id, loomagent.StateActive)
		turn := "turn_" + id
		a.Preset, a.ParentAgentID, a.RootAgentID, a.RunningTurnID = "task", &parent, &parent, &turn
		return a
	}
	other := testAgent("x1", loomagent.StateIdle)
	other.WorkspaceID = "ws2"
	for _, a := range []loomstore.Agent{testAgent("a1", loomagent.StateIdle), testAgent("b1", loomagent.StateIdle),
		child("c1", "a1"), child("c2", "b1"), other} {
		if err := st.InsertAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	svcs := map[string]*loomagent.Service{
		"ws":  loomagent.New(loomagent.ServiceConfig{Store: st, WorkspaceID: "ws"}),
		"ws2": loomagent.New(loomagent.ServiceConfig{Store: st, WorkspaceID: "ws2"}),
	}
	tokens := NewTokens([]byte(strings.Repeat("k", 32)))
	mux := http.NewServeMux()
	ws := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(middleware.WithWorkspace(r.Context(), r.PathValue("ws"))))
		})
	}
	New(func(id string) *loomagent.Service { return svcs[id] }, nil).WithTokens(tokens).Register(mux, ws, nil)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, tokens
}

// as calls the API with the given Authorization header value ("" for none).
func as(t *testing.T, srv *httptest.Server, auth, method, path, body string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+"/api/workspaces/"+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Idempotency-Key", "k-"+method+path+body)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func ids(out map[string]any) []string {
	var got []string
	agents, _ := out["agents"].([]any)
	for _, a := range agents {
		got = append(got, a.(map[string]any)["agent_id"].(string))
	}
	return got
}

// TestBridgeIdentityFromTokenOnly: only a valid server-issued token for this
// workspace and a live agent names an agent caller. A forged, foreign-key,
// other-workspace or unknown-agent token fails with 401; headers, body
// fields and metadata never name the caller.
func TestBridgeIdentityFromTokenOnly(t *testing.T) {
	srv, tokens := newBridgeServer(t)
	forged := NewTokens([]byte(strings.Repeat("x", 32)))
	tok, b1 := tokens.Agent("ws", "a1"), tokens.Agent("ws", "b1")
	for name, auth := range map[string]string{
		"forged key":      "Bearer " + forged.Agent("ws", "a1"),
		"other workspace": "Bearer " + tokens.Agent("ws2", "x1"),
		"unknown agent":   "Bearer " + tokens.Agent("ws", "zz"),
		"agent elsewhere": "Bearer " + tokens.Agent("ws", "x1"),
		"tampered":        "Bearer " + tok[:len(tok)-2] + "AA",
		"swapped body":    "Bearer " + b1[:strings.LastIndex(b1, ".")] + tok[strings.LastIndex(tok, "."):],
		"garbage":         "Bearer " + tokenPrefix + "nope",
	} {
		status, out := as(t, srv, auth, "GET", "ws/v1/agents", "")
		want(t, name, status, out, 401, "")
	}
	// A token never answers for another workspace's route.
	status, out := as(t, srv, "Bearer "+tok, "GET", "ws2/v1/agents", "")
	want(t, "token used on another workspace", status, out, 401, "")

	// The bridge sees only its own children, whatever the query or headers say.
	req, _ := http.NewRequest("GET", srv.URL+"/api/workspaces/ws/v1/agents?parent=b1", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Loom-Agent-Id", "b1")
	req.Header.Set("X-Opencode-Session-Id", "ses_b1")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out = map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if got := ids(out); resp.StatusCode != 200 || len(got) != 1 || got[0] != "c1" {
		t.Fatalf("bridge list = %d %v; want only c1", resp.StatusCode, got)
	}

	// The sender is the token's agent, not the body.
	status, out = as(t, srv, "Bearer "+tok, "POST", "ws/v1/agents/c1/messages",
		`{"text":"hi","actor":{"kind":"agent","id":"b1"},"sender":"agent:b1"}`)
	want(t, "bridge send", status, out, 202, "")
	if w := waiting(t, srv, "c1"); len(w) != 1 || w[0].(map[string]any)["sender"] != "agent:a1" {
		t.Fatalf("waiting = %v; want one from agent:a1", w)
	}

	// The daemon is its own system actor, not a user or an agent, and is not
	// limited to children.
	status, out = as(t, srv, "Bearer "+tokens.Daemon("ws"), "POST", "ws/v1/agents/c2/messages", `{"text":"go"}`)
	want(t, "daemon send", status, out, 202, "")
	if w := waiting(t, srv, "c2"); len(w) != 1 || w[0].(map[string]any)["sender"] != "system:daemon" {
		t.Fatalf("waiting = %v; want one from system:daemon", w)
	}
	status, out = as(t, srv, "Bearer "+tokens.Daemon("ws2"), "GET", "ws/v1/agents", "")
	want(t, "daemon token for another workspace", status, out, 401, "")

	// No token is the local user, as before.
	status, out = as(t, srv, "", "GET", "ws/v1/agents", "")
	if got := ids(out); status != 200 || len(got) != 4 {
		t.Fatalf("user list = %d %v; want all four", status, got)
	}
}

// TestBridgeOwnChildrenOnly: every per-agent route answers agent_not_found
// for another lead's child, the caller itself, or an unknown agent; the
// caller's own child works.
func TestBridgeOwnChildrenOnly(t *testing.T) {
	srv, tokens := newBridgeServer(t)
	auth := "Bearer " + tokens.Agent("ws", "a1")
	for _, target := range []string{"c2", "b1", "a1", "zz"} {
		for _, rt := range [][3]string{
			{"GET", "", ""},
			{"PATCH", "", `{"name":"stolen"}`},
			{"DELETE", "", ""},
			{"POST", "/archive", ""},
			{"POST", "/unarchive", ""},
			{"POST", "/messages", `{"text":"hi"}`},
			{"POST", "/messages", `{"delivery":"interrupt"}`},
			{"DELETE", "/messages/waiting", ""},
			{"POST", "/asks/ask_1", `{"decision":"allow_once"}`},
			{"GET", "/events", ""},
		} {
			status, out := as(t, srv, auth, rt[0], "ws/v1/agents/"+target+rt[1], rt[2])
			want(t, rt[0]+" "+target+rt[1], status, out, 404, "agent_not_found")
		}
	}
	if w := waiting(t, srv, "c2"); len(w) != 0 {
		t.Fatalf("another lead's child got %v", w)
	}
	status, out := as(t, srv, auth, "GET", "ws/v1/agents/c1", "")
	if status != 200 || out["agent_id"] != "c1" {
		t.Fatalf("own child = %d %v", status, out)
	}
	status, out = as(t, srv, auth, "PATCH", "ws/v1/agents/c1", `{"name":"mine"}`)
	if status != 200 || out["name"] != "mine" {
		t.Fatalf("rename own child = %d %v", status, out)
	}
}

// TestBridgeIdentityKeyPersists: the token key is created once (0600) and
// reused, so tokens issued before a serve restart stay valid.
func TestBridgeIdentityKeyPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-token.key")
	first, err := LoadTokens(path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadTokens(path)
	if err != nil {
		t.Fatal(err)
	}
	if first.Agent("ws", "a1") != again.Agent("ws", "a1") {
		t.Fatal("a reloaded key issues different tokens")
	}
	if c, ws, ok := again.verify(first.Daemon("ws")); !ok || ws != "ws" || c != (loomagent.ActorRef{Kind: "system", ID: "daemon"}) {
		t.Fatalf("daemon token = %v %q %v", c, ws, ok)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file = %v %v; want mode 0600", fi, err)
	}
	if _, err := os.Stat(path + ".new"); !os.IsNotExist(err) {
		t.Fatalf("temp key left behind: %v", err)
	}
}
