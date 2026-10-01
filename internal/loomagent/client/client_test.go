package client

import (
	"context"
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
// request without one is the local user.
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
	mux := http.NewServeMux()
	agentsv1.New(func(ws string) *loomagent.Service {
		if ws == "ws" {
			return svc
		}
		return nil
	}, nil).Register(mux, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := middleware.WithWorkspace(r.Context(), r.PathValue("ws"))
			if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
				ctx = middleware.WithUserIdentity(ctx, middleware.UserIdentity{UserID: tok})
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		srv.Close()
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

// TestClientEveryMethod drives every Agent API method through the client
// against the real routes on a fake harness.
func TestClientEveryMethod(t *testing.T) {
	ctx := context.Background()
	srv, fh := newServer(t)
	c := newClient(srv, "ws", "")

	a, err := c.Create(ctx, loomagent.CreateRequest{Envelope: loomagent.Envelope{RequestID: "c1"}, Preset: "lead",
		Name: "alpha", Repo: "/repo", Overrides: loomagent.Overrides{Harness: "opencode"}})
	if err != nil || a.AgentID == "" || a.Name != "alpha" {
		t.Fatalf("Create = %+v, %v", a, err)
	}
	again, err := c.Create(ctx, loomagent.CreateRequest{Envelope: loomagent.Envelope{RequestID: "c1"}, Preset: "lead",
		Name: "alpha", Repo: "/repo", Overrides: loomagent.Overrides{Harness: "opencode"}})
	if err != nil || again.AgentID != a.AgentID {
		t.Fatalf("Create retry = %+v, %v; want %s", again, err, a.AgentID)
	}
	id := a.AgentID
	eventually(t, "idle", func() bool { got, err := c.Get(ctx, id); return err == nil && got.State == loomagent.StateIdle })

	agents, next, err := c.List(ctx, loomstore.AgentFilter{Name: "alpha", Limit: 10})
	if err != nil || len(agents) != 1 || agents[0].AgentID != id || next != "" {
		t.Fatalf("List = %+v, %q, %v", agents, next, err)
	}
	spec := a.SpecVersion
	u, err := c.Update(ctx, loomagent.UpdateRequest{Envelope: loomagent.Envelope{RequestID: "u1",
		Expect: &loomagent.Expect{SpecVersion: &spec}}, AgentID: id, Name: "beta"})
	if err != nil || u.Name != "beta" {
		t.Fatalf("Update = %+v, %v", u, err)
	}
	if _, err := c.Update(ctx, loomagent.UpdateRequest{Envelope: loomagent.Envelope{RequestID: "u2",
		Expect: &loomagent.Expect{SpecVersion: &spec}}, AgentID: id, Name: "gamma"}); code(err) != loomagent.CodeSpecVersionMismatch {
		t.Fatalf("stale Update = %v; want spec_version_mismatch", err)
	}

	fh.Script(id, fake.Turn{Steps: []fake.Step{{Delta: "thinking"}, {Ask: "k1"}, {Delta: "done"}}})
	sent, err := c.Send(ctx, loomagent.SendRequest{Envelope: loomagent.Envelope{RequestID: "s1"}, AgentID: id, Text: "hi"})
	if err != nil || sent.MessageID == "" {
		t.Fatalf("Send = %+v, %v", sent, err)
	}
	retry, err := c.Send(ctx, loomagent.SendRequest{Envelope: loomagent.Envelope{RequestID: "s1"}, AgentID: id, Text: "hi"})
	if err != nil || retry.MessageID != sent.MessageID {
		t.Fatalf("Send retry = %+v, %v; want %+v", retry, err, sent)
	}
	eventually(t, "ask k1", func() bool { got, err := c.Get(ctx, id); return err == nil && len(got.OpenAsks) == 1 })
	if _, err := c.Send(ctx, loomagent.SendRequest{Envelope: loomagent.Envelope{RequestID: "s2"}, AgentID: id, Text: "next"}); err != nil {
		t.Fatal(err)
	}
	if w, err := c.Withdraw(ctx, loomagent.WithdrawRequest{Envelope: loomagent.Envelope{RequestID: "w1"}, AgentID: id}); err != nil || w.Result != "withdrawn" {
		t.Fatalf("Withdraw = %+v, %v", w, err)
	}
	if err := c.Respond(ctx, loomagent.RespondRequest{Envelope: loomagent.Envelope{RequestID: "r1"}, AgentID: id,
		AskID: "k1", Decision: "allow_once"}); err != nil {
		t.Fatalf("Respond = %v", err)
	}
	eventually(t, "turn end", func() bool { got, err := c.Get(ctx, id); return err == nil && got.State == loomagent.StateIdle })

	all, err := c.ListEvents(ctx, loomstore.EventQuery{AgentID: id})
	if err != nil || len(all.Events) < 3 || all.More {
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
	if p, err := c.Preset(ctx, "lead"); err != nil || p.Name != "lead" {
		t.Fatalf("Preset = %+v, %v", p, err)
	}
	if err := c.Archive(ctx, loomagent.ArchiveRequest{Envelope: loomagent.Envelope{RequestID: "a1"}, AgentID: id}); err != nil {
		t.Fatalf("Archive = %v", err)
	}
	if _, _, err := c.List(ctx, loomstore.AgentFilter{}); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := c.List(ctx, loomstore.AgentFilter{IncludeArchived: true}); len(got) != 1 || got[0].State != loomagent.StateArchived {
		t.Fatalf("List archived = %+v", got)
	}
	if err := c.Unarchive(ctx, loomagent.ArchiveRequest{Envelope: loomagent.Envelope{RequestID: "a2"}, AgentID: id}); err != nil {
		t.Fatalf("Unarchive = %v", err)
	}
	if err := c.Delete(ctx, loomagent.DeleteRequest{Envelope: loomagent.Envelope{RequestID: "d1"}, AgentID: id}); err != nil {
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
	_, err := c.Create(ctx, loomagent.CreateRequest{Envelope: loomagent.Envelope{RequestID: "c1"}, Preset: "lead",
		Name: "x", Repo: "/repo", Overrides: loomagent.Overrides{Harness: "nope"}})
	var e *loomagent.Error
	if !errors.As(err, &e) || e.Code != loomagent.CodePresetInvalid || len(e.Allowed) == 0 {
		t.Fatalf("Create bad harness = %#v; want preset_invalid with allowed", err)
	}
	var se *StatusError
	if _, err := newClient(srv, "other", "").Get(ctx, "a"); !errors.As(err, &se) || se.Status != http.StatusNotFound {
		t.Fatalf("other workspace = %v; want a 404 StatusError", err)
	}
	if _, err := c.ListEvents(ctx, loomstore.EventQuery{AgentID: "nope"}); err == nil {
		t.Fatal("ListEvents of an unknown agent succeeded")
	}
	boom := errors.New("no token")
	bad := New(Config{BaseURL: srv.URL, Workspace: "ws", HTTP: srv.Client(),
		Token: func(context.Context) (string, error) { return "", boom }})
	if _, err := bad.Get(ctx, "a"); !errors.Is(err, boom) {
		t.Fatalf("token failure = %v", err)
	}
}

// TestClientActorFromAuth checks that the caller is the authenticated user,
// never an actor the client could put in a request.
func TestClientActorFromAuth(t *testing.T) {
	ctx := context.Background()
	srv, fh := newServer(t)
	alice := newClient(srv, "ws", "alice")

	req := loomagent.CreateRequest{Envelope: loomagent.Envelope{RequestID: "c1"}, Preset: "lead", Name: "alpha",
		Repo: "/repo", Overrides: loomagent.Overrides{Harness: "opencode"}, Actor: loomagent.ActorRef{Kind: "user", ID: "mallory"}}
	a, err := alice.Create(ctx, req)
	if err != nil || a.OwnerID != "alice" || a.CreatedByID != "alice" {
		t.Fatalf("Create = owner %q by %q, %v; want alice", a.OwnerID, a.CreatedByID, err)
	}
	fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "k1"}}})
	eventually(t, "idle", func() bool {
		got, err := alice.Get(ctx, a.AgentID)
		return err == nil && got.State == loomagent.StateIdle
	})
	if _, err := alice.Send(ctx, loomagent.SendRequest{Envelope: loomagent.Envelope{RequestID: "s1"}, AgentID: a.AgentID,
		Text: "first"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "busy", func() bool {
		got, err := alice.Get(ctx, a.AgentID)
		return err == nil && got.State == loomagent.StateWaiting
	})
	if _, err := alice.Send(ctx, loomagent.SendRequest{Envelope: loomagent.Envelope{RequestID: "s2"}, AgentID: a.AgentID,
		Text: "from alice", Actor: loomagent.ActorRef{Kind: "agent", ID: "mallory"}, Source: "system"}); err != nil {
		t.Fatal(err)
	}
	got, err := alice.Get(ctx, a.AgentID)
	if err != nil || len(got.WaitingMessages) != 1 || !strings.Contains(got.WaitingMessages[0].Sender, "alice") {
		t.Fatalf("waiting = %+v, %v; want alice's", got.WaitingMessages, err)
	}
	// The local user has no waiting message to withdraw; alice's stays.
	if w, err := newClient(srv, "ws", "").Withdraw(ctx, loomagent.WithdrawRequest{Envelope: loomagent.Envelope{RequestID: "w1"},
		AgentID: a.AgentID, Actor: loomagent.ActorRef{Kind: "user", ID: "alice"}}); err != nil || w.Result != "nothing_waiting" {
		t.Fatalf("local Withdraw = %+v, %v; want nothing_waiting", w, err)
	}
}
