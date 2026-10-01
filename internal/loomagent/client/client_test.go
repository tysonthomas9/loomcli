package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentsv1"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/subscription"
)

type workspace struct{}

func (workspace) Ensure(_ context.Context, s loomagent.WorkspaceSpec) (loomagent.WorkingCopy, error) {
	return loomagent.WorkingCopy{Path: "/wt/" + s.Key, Branch: s.Branch, HEAD: "abc"}, nil
}

func (workspace) Status(_ context.Context, s loomagent.WorkspaceSpec) (loomagent.WorkspaceStatus, error) {
	return loomagent.WorkspaceStatus{Branch: s.Branch, HEAD: "abc"}, nil
}

func (workspace) Remove(context.Context, loomagent.WorkspaceSpec) error { return nil }

func (workspace) Publish(context.Context, loomagent.PublishRequest) (loomagent.PublishResult, error) {
	return loomagent.PublishResult{}, nil
}

// newServer serves workspace "ws" through the agentsv1 routes on a fake
// OpenCode harness. A bearer token is taken as the signed-in user's id; a
// request without one is the local user. The event stream needs a one-time
// token from the webui SSE token route.
func newServer(t *testing.T) (*httptest.Server, *fake.Harness) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	st, err := loomstore.Open(ctx, filepath.Join(t.TempDir(), "loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	fh := fake.New()
	svc := loomagent.New(loomagent.ServiceConfig{Store: st, WorkspaceID: "ws", Workspace: workspace{},
		Harnesses: map[string]loomharness.Harness{"opencode": fh},
		Bridge: func(context.Context, loomagent.Preset) (loomagent.BridgeCaps, error) {
			return loomagent.BridgeCaps{}, nil
		},
		Launch: func(context.Context, loomstore.Agent, string) (loomharness.Launch, error) {
			return loomharness.Launch{Root: "/root/opencode"}, nil
		}})
	done := make(chan struct{})
	go func() { defer close(done); svc.RunFeed(ctx, "opencode") }()
	tokens, err := realtime.NewTokenStore()
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := middleware.WithWorkspace(r.Context(), r.PathValue("ws"))
			if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
				ctx = middleware.WithUserIdentity(ctx, middleware.UserIdentity{UserID: tok})
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	mux.Handle("GET /api/workspaces/{ws}/events/token", auth(subscription.HandleSSEToken(tokens)))
	agentsv1.New(func(ws string) *loomagent.Service {
		if ws == "ws" {
			return svc
		}
		return nil
	}, nil).Register(mux, auth, tokens.Validate)
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		srv.Close()
		tokens.Stop()
		cancel()
		<-done
		st.Close()
	})
	return srv, fh
}

func newClient(srv *httptest.Server, ws, token string) *Client {
	return New(Config{BaseURL: srv.URL + "/", Workspace: ws, HTTP: srv.Client(),
		Token: func(context.Context) (string, error) { return token, nil }})
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); !ok(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func code(err error) loomagent.Code {
	var e *loomagent.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func lead(name string) agentsv1.CreateBody {
	return agentsv1.CreateBody{Preset: "lead", Name: name, Repo: "/repo", Overrides: agentsv1.Overrides{Harness: "opencode"}}
}

// TestClientEveryMethod drives every Agent API method through the client
// against the real routes on a fake harness.
func TestClientEveryMethod(t *testing.T) {
	ctx := context.Background()
	srv, fh := newServer(t)
	c := newClient(srv, "ws", "")

	a, err := c.Create(ctx, "c1", lead("alpha"))
	if err != nil || a.AgentID == "" || a.Name != "alpha" {
		t.Fatalf("Create = %+v, %v", a, err)
	}
	if again, err := c.Create(ctx, "c1", lead("alpha")); err != nil || again.AgentID != a.AgentID {
		t.Fatalf("Create retry = %+v, %v; want %s", again, err, a.AgentID)
	}
	id := a.AgentID
	eventually(t, "idle", func() bool { got, err := c.Get(ctx, id); return err == nil && got.State == loomagent.StateIdle })

	l, err := c.List(ctx, loomstore.AgentFilter{Name: "alpha", Limit: 10})
	if err != nil || len(l.Agents) != 1 || l.Agents[0].AgentID != id || l.Next != "" {
		t.Fatalf("List = %+v, %v", l, err)
	}
	spec := a.SpecVersion
	u, err := c.Update(ctx, "u1", id, agentsv1.UpdateBody{Name: "beta", Expect: &agentsv1.Expect{SpecVersion: &spec}})
	if err != nil || u.Name != "beta" {
		t.Fatalf("Update = %+v, %v", u, err)
	}
	if _, err := c.Update(ctx, "u2", id, agentsv1.UpdateBody{Name: "gamma",
		Expect: &agentsv1.Expect{SpecVersion: &spec}}); code(err) != loomagent.CodeSpecVersionMismatch {
		t.Fatalf("stale Update = %v; want spec_version_mismatch", err)
	}

	fh.Script(id, fake.Turn{Steps: []fake.Step{{Delta: "thinking"}, {Ask: "k1"}, {Delta: "done"}}})
	sent, err := c.Send(ctx, "s1", id, "hi")
	if err != nil || sent.MessageID == "" {
		t.Fatalf("Send = %+v, %v", sent, err)
	}
	if retry, err := c.Send(ctx, "s1", id, "hi"); err != nil || retry.MessageID != sent.MessageID {
		t.Fatalf("Send retry = %+v, %v; want %+v", retry, err, sent)
	}
	eventually(t, "ask k1", func() bool { got, err := c.Get(ctx, id); return err == nil && len(got.OpenAsks) == 1 })
	if _, err := c.Send(ctx, "s2", id, "next"); err != nil {
		t.Fatal(err)
	}
	if got, err := c.Get(ctx, id); err != nil || len(got.WaitingMessages) != 1 || got.WaitingMessages[0].Text != "next" ||
		got.OpenAsks[0].ID != "k1" {
		t.Fatalf("Get = %+v, %v; want waiting next and ask k1", got, err)
	}
	if w, err := c.Withdraw(ctx, "w1", id); err != nil || w.Result != "withdrawn" {
		t.Fatalf("Withdraw = %+v, %v", w, err)
	}
	if err := c.Respond(ctx, "r1", id, "k1", agentsv1.RespondBody{Decision: "allow_once"}); err != nil {
		t.Fatalf("Respond = %v", err)
	}
	eventually(t, "turn end", func() bool { got, err := c.Get(ctx, id); return err == nil && got.State == loomagent.StateIdle })

	all, err := c.ListEvents(ctx, loomstore.EventQuery{AgentID: id})
	if err != nil || len(all.Events) < 3 || all.More || all.SnapshotSeq == 0 || all.Events[0].EventID == "" {
		t.Fatalf("ListEvents = %+v, %v", all, err)
	}
	// Read one event at a time, dropping the connection between pages: each
	// resume from the last cursor returns the next event exactly once.
	var seqs []int64
	for after := int64(0); ; {
		p, err := c.ListEvents(ctx, loomstore.EventQuery{AgentID: id, After: after, Snapshot: all.SnapshotSeq, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range p.Events {
			seqs = append(seqs, e.Seq)
		}
		srv.CloseClientConnections()
		if !p.More {
			break
		}
		after = p.Next
	}
	if len(seqs) != len(all.Events) {
		t.Fatalf("paged seqs %v; want %d events", seqs, len(all.Events))
	}
	for i, e := range all.Events {
		if seqs[i] != e.Seq {
			t.Fatalf("paged seqs %v differ from %+v", seqs, all.Events)
		}
	}
	kinds, err := c.ListEvents(ctx, loomstore.EventQuery{AgentID: id, Kinds: []string{string(loomharness.EventAskResolved)}})
	if err != nil || len(kinds.Events) != 1 || kinds.Events[0].Kind != string(loomharness.EventAskResolved) {
		t.Fatalf("ListEvents kind = %+v, %v", kinds, err)
	}

	ps, err := c.Presets(ctx)
	if err != nil || len(ps) == 0 {
		t.Fatalf("Presets = %v, %v", ps, err)
	}
	if p, err := c.Preset(ctx, "lead"); err != nil || p.Name != "lead" || len(p.Rules) == 0 {
		t.Fatalf("Preset = %+v, %v", p, err)
	}
	if err := c.Archive(ctx, "a1", id, ""); err != nil {
		t.Fatalf("Archive = %v", err)
	}
	if l, err := c.List(ctx, loomstore.AgentFilter{}); err != nil || len(l.Agents) != 0 {
		t.Fatalf("List = %+v, %v; want no live agents", l, err)
	}
	if l, _ := c.List(ctx, loomstore.AgentFilter{IncludeArchived: true}); len(l.Agents) != 1 || l.Agents[0].State != loomagent.StateArchived {
		t.Fatalf("List archived = %+v", l)
	}
	if err := c.Unarchive(ctx, "a2", id); err != nil {
		t.Fatalf("Unarchive = %v", err)
	}
	if err := c.Delete(ctx, "d1", id, false, ""); err != nil {
		t.Fatalf("Delete = %v", err)
	}
	if got, err := c.Get(ctx, id); err != nil || got.DeletedAt == nil {
		t.Fatalf("Get after Delete = %+v, %v; want a tombstone", got, err)
	}
}

// TestClientTypedErrors checks that error answers come back as typed codes,
// and answers without a code as a StatusError.
func TestClientTypedErrors(t *testing.T) {
	ctx := context.Background()
	srv, _ := newServer(t)
	c := newClient(srv, "ws", "")

	if _, err := c.Get(ctx, "nope"); code(err) != loomagent.CodeAgentNotFound {
		t.Fatalf("Get = %v; want agent_not_found", err)
	}
	if _, err := c.Preset(ctx, "nope"); code(err) != loomagent.CodePresetNotFound {
		t.Fatalf("Preset = %v; want preset_not_found", err)
	}
	b := lead("x")
	b.Overrides.Harness = "nope"
	_, err := c.Create(ctx, "c1", b)
	var e *loomagent.Error
	if !errors.As(err, &e) || e.Code != loomagent.CodePresetInvalid || len(e.Allowed) == 0 {
		t.Fatalf("Create bad harness = %#v; want preset_invalid with allowed", err)
	}
	var se *StatusError
	if _, err := newClient(srv, "other", "").Get(ctx, "a"); !errors.As(err, &se) || se.Status != http.StatusNotFound {
		t.Fatalf("other workspace = %v; want a 404 StatusError", err)
	}
	boom := errors.New("no token")
	bad := New(Config{BaseURL: srv.URL, Workspace: "ws", HTTP: srv.Client(),
		Token: func(context.Context) (string, error) { return "", boom }})
	if _, err := bad.Get(ctx, "a"); !errors.Is(err, boom) {
		t.Fatalf("token failure = %v", err)
	}
}

// TestClientActorFromAuth checks that the caller is the authenticated user:
// the client has no way to name an actor, and the server takes the token's.
func TestClientActorFromAuth(t *testing.T) {
	ctx := context.Background()
	srv, fh := newServer(t)
	alice := newClient(srv, "ws", "alice")

	a, err := alice.Create(ctx, "c1", lead("alpha"))
	if err != nil || a.OwnerID != "alice" || a.CreatedByID != "alice" {
		t.Fatalf("Create = owner %q by %q, %v; want alice", a.OwnerID, a.CreatedByID, err)
	}
	fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "k1"}}})
	eventually(t, "idle", func() bool {
		got, err := alice.Get(ctx, a.AgentID)
		return err == nil && got.State == loomagent.StateIdle
	})
	if _, err := alice.Send(ctx, "s1", a.AgentID, "first"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "busy", func() bool {
		got, err := alice.Get(ctx, a.AgentID)
		return err == nil && got.State == loomagent.StateWaiting
	})
	if _, err := alice.Send(ctx, "s2", a.AgentID, "from alice"); err != nil {
		t.Fatal(err)
	}
	got, err := alice.Get(ctx, a.AgentID)
	if err != nil || len(got.WaitingMessages) != 1 || !strings.Contains(got.WaitingMessages[0].Sender, "alice") {
		t.Fatalf("waiting = %+v, %v; want alice's", got.WaitingMessages, err)
	}
	// The local user has no waiting message to withdraw; alice's stays.
	if w, err := newClient(srv, "ws", "").Withdraw(ctx, "w1", a.AgentID); err != nil || w.Result != "nothing_waiting" {
		t.Fatalf("local Withdraw = %+v, %v; want nothing_waiting", w, err)
	}
}

// TestClientWireKeys checks the literal request a write sends: snake_case
// body keys, the RequestID only in Idempotency-Key, and the bearer token.
func TestClientWireKeys(t *testing.T) {
	var got struct {
		method, path, key, auth string
		body                    map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.key, got.auth = r.Method, r.URL.EscapedPath(), r.Header.Get("Idempotency-Key"), r.Header.Get("Authorization")
		got.body = nil
		if err := json.NewDecoder(r.Body).Decode(&got.body); err != nil {
			t.Errorf("body: %v", err)
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()
	c := newClient(srv, "w s", "tok")
	ctx := context.Background()

	if _, err := c.Create(ctx, "req-1", agentsv1.CreateBody{Preset: "lead", Name: "n", BaseRef: "main",
		FirstMessage: "hi", ExternalKey: "k", Overrides: agentsv1.Overrides{Harness: "opencode", ReadOnly: true}}); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/api/workspaces/w%20s/v1/agents" || got.key != "req-1" || got.auth != "Bearer tok" {
		t.Fatalf("Create sent %s %s key %q auth %q", got.method, got.path, got.key, got.auth)
	}
	o, _ := got.body["overrides"].(map[string]any)
	if got.body["base_ref"] != "main" || got.body["first_message"] != "hi" || got.body["external_key"] != "k" ||
		o["harness"] != "opencode" || o["read_only"] != true {
		t.Fatalf("Create body = %v; want snake_case keys", got.body)
	}
	for k := range got.body {
		if k != strings.ToLower(k) || strings.Contains(strings.ToLower(k), "actor") || strings.Contains(strings.ToLower(k), "request") {
			t.Fatalf("Create body key %q", k)
		}
	}
	spec := int64(3)
	if _, err := c.Update(ctx, "req-2", "a/1", agentsv1.UpdateBody{Model: "m", Expect: &agentsv1.Expect{SpecVersion: &spec}}); err != nil {
		t.Fatal(err)
	}
	e, _ := got.body["expect"].(map[string]any)
	if got.method != "PATCH" || got.path != "/api/workspaces/w%20s/v1/agents/a%2F1" || got.key != "req-2" || e["spec_version"] != float64(3) {
		t.Fatalf("Update sent %s %s key %q body %v", got.method, got.path, got.key, got.body)
	}
	if _, err := c.Send(ctx, "req-3", "a1", "hello"); err != nil {
		t.Fatal(err)
	}
	if len(got.body) != 1 || got.body["text"] != "hello" || got.key != "req-3" {
		t.Fatalf("Send body = %v key %q; want only text", got.body, got.key)
	}
}
