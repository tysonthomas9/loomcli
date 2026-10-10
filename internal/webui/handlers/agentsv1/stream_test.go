package agentsv1

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

type streamEnv struct {
	srv *httptest.Server
	h   *Handler
	ev  *loomagent.EventLog
	n   int
	mu  sync.Mutex
}

// newStreamEnv serves workspace "ws" with agents a1 and b1 and an agent p1
// whose history was purged. tokens, when set, guards the stream.
func newStreamEnv(t *testing.T, tokens *realtime.TokenStore) *streamEnv {
	t.Helper()
	ctx := context.Background()
	st, err := loomstore.Open(ctx, filepath.Join(t.TempDir(), "loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	purged := testAgent("p1", loomagent.StateArchived)
	stamp := loomstore.Stamp(time.Now())
	purged.HistoryPurgedAt = &stamp
	for _, a := range []loomstore.Agent{testAgent("a1", loomagent.StateIdle), testAgent("b1", loomagent.StateIdle), purged} {
		if err := st.InsertAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	e := &streamEnv{ev: loomagent.NewEventLog(st)}
	svc := loomagent.New(loomagent.ServiceConfig{Store: st, Events: e.ev, WorkspaceID: "ws"})
	e.h = New(func(id string) *loomagent.Service {
		if id == "ws" {
			return svc
		}
		return nil
	}, nil)
	mux := http.NewServeMux()
	var validate func(token, workspace string) (string, error)
	if tokens != nil {
		validate = tokens.Validate
	}
	e.h.Register(mux, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(middleware.WithWorkspace(r.Context(), r.PathValue("ws"))))
		})
	}, validate)
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

// commit saves one event of kind for agent.
func (e *streamEnv) commit(t *testing.T, agent, kind string) {
	t.Helper()
	e.mu.Lock()
	e.n++
	id := fmt.Sprintf("evt_%d", e.n)
	e.mu.Unlock()
	if _, err := e.ev.Append(context.Background(), loomstore.Event{AgentID: agent, EventID: id, Kind: kind,
		Payload: json.RawMessage(`{"n":"` + id + `"}`)}); err != nil {
		t.Error(err)
	}
}

// frame is one SSE frame; kind is the data's kind, for a StreamEvent frame.
type frame struct{ id, event, data, kind string }

type sseConn struct {
	resp *http.Response
	rd   *bufio.Reader
}

func (e *streamEnv) open(t *testing.T, query string) (*sseConn, int, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.srv.URL+"/api/workspaces/ws/v1/events?"+query, nil)
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		code, _ := body["code"].(string)
		return nil, resp.StatusCode, code
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	return &sseConn{resp, bufio.NewReader(resp.Body)}, http.StatusOK, ""
}

// next reads one frame (comments skipped).
func (c *sseConn) next(t *testing.T) frame {
	t.Helper()
	var f frame
	for {
		line, err := c.rd.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		line = strings.TrimSuffix(line, "\n")
		switch {
		case line == "" && f.event != "":
			if f.event != StreamEvent {
				t.Fatalf("frame named %q; want %q", f.event, StreamEvent)
			}
			f.kind = f.decode(t).Kind
			return f
		case strings.HasPrefix(line, "id: "):
			f.id = line[4:]
		case strings.HasPrefix(line, "event: "):
			f.event = line[7:]
		case strings.HasPrefix(line, "data: "):
			f.data = line[6:]
		}
	}
}

func (f frame) decode(t *testing.T) Event {
	t.Helper()
	var e Event
	if err := json.Unmarshal([]byte(f.data), &e); err != nil {
		t.Fatalf("data %q: %v", f.data, err)
	}
	return e
}

// TestAgentSSETokenAuth: with tokens on, a missing, forged, reused or
// other-workspace token is refused; a fresh token streams.
func TestAgentSSETokenAuth(t *testing.T) {
	tokens, err := realtime.NewTokenStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tokens.Stop)
	e := newStreamEnv(t, tokens)
	if _, status, _ := e.open(t, "agents=a1"); status != http.StatusUnauthorized {
		t.Fatalf("no token = %d", status)
	}
	zz, _ := tokens.Generate("u", "ws")
	if _, status, code := e.open(t, "agents=zz&token="+zz); status != http.StatusNotFound || code != "agent_not_found" {
		t.Fatalf("unknown agent with a fresh token = %d %q", status, code)
	}
	if _, status, _ := e.open(t, "agents=a1&token=forged.x"); status != http.StatusUnauthorized {
		t.Fatalf("forged token = %d", status)
	}
	other, _ := tokens.Generate("u", "other")
	if _, status, _ := e.open(t, "agents=a1&token="+other); status != http.StatusUnauthorized {
		t.Fatalf("other workspace's token = %d", status)
	}
	tok, _ := tokens.Generate("u", "ws")
	c, status, _ := e.open(t, "agents=a1&token="+tok)
	if status != http.StatusOK {
		t.Fatalf("fresh token = %d", status)
	}
	e.commit(t, "a1", "item.completed")
	if f := c.next(t); f.kind != "item.completed" || f.id != "a1:1" {
		t.Fatalf("frame = %+v", f)
	}
	if _, status, _ := e.open(t, "agents=a1&token="+tok); status != http.StatusUnauthorized {
		t.Fatalf("reused token = %d", status)
	}
}

// TestAgentSSECursorReplayExactlyOnce: a stream replays committed events
// after its cursor and then goes live while events commit concurrently; a
// reconnect from the last seq received gets every remaining event once,
// across a feed gap. Filters keep other agents, other kinds and (unless
// asked) deltas out.
func TestAgentSSECursorReplayExactlyOnce(t *testing.T) {
	e := newStreamEnv(t, nil)
	for range 3 {
		e.commit(t, "a1", "item.completed")
	}
	const total = 60
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 4; i <= total; i++ {
			e.commit(t, "a1", "item.completed")
			e.commit(t, "b1", "item.completed")
			if i == 30 {
				e.ev.Notify(loomstore.Event{Kind: loomagent.KindFeedGap})
			}
		}
	}()
	got := map[int64]int{}
	read := func(c *sseConn, until int64) int64 {
		var last int64
		for last < until {
			f := c.next(t)
			if f.kind == loomagent.KindFeedGap {
				if f.id != "" {
					t.Fatalf("feed.gap has id %q", f.id)
				}
				continue
			}
			ev := f.decode(t)
			if ev.AgentID != "a1" || f.id != fmt.Sprintf("a1:%d", ev.Seq) || ev.Kind != f.kind {
				t.Fatalf("frame %+v", f)
			}
			got[ev.Seq]++
			last = ev.Seq
		}
		return last
	}
	c, _, _ := e.open(t, "agents=a1&after=a1:1")
	last := read(c, 25)
	c.resp.Body.Close()
	c, _, _ = e.open(t, fmt.Sprintf("agents=a1&after=a1:%d", last))
	read(c, total)
	<-done
	for seq := int64(2); seq <= total; seq++ {
		if got[seq] != 1 {
			t.Fatalf("seq %d delivered %d times", seq, got[seq])
		}
	}
	if len(got) != total-1 {
		t.Fatalf("delivered %d events; want %d", len(got), total-1)
	}

	// Filters: only types asked for; deltas only with deltas=true.
	c, _, _ = e.open(t, "agents=a1&types=agent.updated")
	d, _, _ := e.open(t, "agents=a1&deltas=true")
	e.ev.Notify(loomstore.Event{AgentID: "a1", Kind: loomagent.KindDelta, Payload: json.RawMessage(`{"text":"x"}`)})
	e.ev.Notify(loomstore.Event{AgentID: "b1", Kind: loomagent.KindDelta, Payload: json.RawMessage(`{"text":"y"}`)})
	e.commit(t, "a1", "item.completed")
	e.commit(t, "a1", "agent.updated")
	if f := c.next(t); f.kind != "agent.updated" {
		t.Fatalf("types filter: %+v", f)
	}
	if f := d.next(t); f.kind != loomagent.KindDelta || f.id != "" || f.decode(t).AgentID != "a1" {
		t.Fatalf("delta: %+v", f)
	}
	if f := d.next(t); f.kind != "item.completed" {
		t.Fatalf("after delta: %+v", f)
	}
}

// TestAgentSSECursorErrors: refused subscriptions answer with a code before
// any stream starts.
func TestAgentSSECursorErrors(t *testing.T) {
	e := newStreamEnv(t, nil)
	for _, tc := range []struct{ query, code string }{
		{"agents=p1&after=p1:5", "cursor_expired"},
		{"agents=zz&after=zz:0", "agent_not_found"},
		{"agents=zz", "agent_not_found"},
		{"agents=a1,zz&after=a1:0", "agent_not_found"},
		{"", ""},
		{"agents=a1&after=a1", ""},
		{"agents=a1&after=a1:x", ""},
	} {
		_, status, code := e.open(t, tc.query)
		if status == http.StatusOK || code != tc.code {
			t.Errorf("%q = %d %q; want code %q", tc.query, status, code, tc.code)
		}
	}
	if _, status, _ := e.open(t, "agents=p1"); status != http.StatusOK {
		t.Errorf("live-only on purged history = %d; want a stream", status)
	}
}

// blockingWriter holds every write until released, like a client that stops reading.
type blockingWriter struct {
	httptest.ResponseRecorder
	release chan struct{}
	mu      sync.Mutex
	buf     strings.Builder
}

func (w *blockingWriter) Write(b []byte) (int, error) {
	<-w.release
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(b)
}

func (w *blockingWriter) Flush() {}

// TestAgentSSESubscriberLaggedEndsStream: a subscriber that falls behind is
// ended with an error frame carrying subscriber_lagged.
func TestAgentSSESubscriberLaggedEndsStream(t *testing.T) {
	e := newStreamEnv(t, nil)
	w := &blockingWriter{ResponseRecorder: *httptest.NewRecorder(), release: make(chan struct{})}
	r := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws/v1/events?agents=a1", nil)
	r = r.WithContext(middleware.WithWorkspace(r.Context(), "ws"))
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		e.h.stream(w, r)
	}()
	for range 400 {
		e.commit(t, "a1", "item.completed")
	}
	close(w.release)
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		t.Fatal("stream did not end")
	}
	out := w.buf.String()
	if !strings.Contains(out, "event: error\ndata: {\"error\":\"subscriber too slow; reconnect from the last seq\",\"code\":\"subscriber_lagged\"}\n\n") {
		t.Fatalf("no subscriber_lagged frame at the end:\n%s", out[max(0, len(out)-300):])
	}
}
