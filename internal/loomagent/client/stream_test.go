package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// TestClientSubscribeResumesFromCursor: through the real routes and token
// exchange, a Subscribe without a cursor starts after the agent's current
// last event, and every saved event arrives exactly once although the
// connection is dropped after each one, including across turns committed
// while it was down.
func TestClientSubscribeResumesFromCursor(t *testing.T) {
	ctx := context.Background()
	srv, fh := newServer(t)
	c := newClient(srv, "ws", "alice")
	a, err := c.Create(ctx, "c1", lead("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	id := a.AgentID
	eventually(t, "idle", func() bool { got, err := c.Get(ctx, id); return err == nil && got.State == loomagent.StateIdle })
	before, err := c.ListEvents(ctx, loomstore.EventQuery{AgentID: id})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := newClient(srv, "ws", "").Subscribe(ctx, SubscribeRequest{Agents: []string{id}}); err == nil {
		t.Fatal("Subscribe without a signed-in user succeeded")
	}
	s, err := c.Subscribe(ctx, SubscribeRequest{Agents: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var got []int64
	cursor := before.SnapshotSeq
	for turn := range 3 {
		// The previous connection was dropped after its last event; this
		// turn's events are committed only after that, so the stream must
		// reconnect from its cursor to get them.
		fh.Script(id, fake.Turn{Steps: []fake.Step{{Delta: "one"}, {Delta: "two"}}})
		if _, err := c.Send(ctx, fmt.Sprintf("s%d", turn), id, "hi"); err != nil {
			t.Fatal(err)
		}
		eventually(t, "turn end", func() bool { got, err := c.Get(ctx, id); return err == nil && got.State == loomagent.StateIdle })
		page, err := c.ListEvents(ctx, loomstore.EventQuery{AgentID: id, After: cursor})
		if err != nil || len(page.Events) < 3 {
			t.Fatalf("turn %d events = %+v, %v", turn, page, err)
		}
		for _, e := range page.Events {
			n, err := s.Next()
			for err == nil && n.Seq == 0 {
				n, err = s.Next()
			}
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, n.Seq)
			if n.Seq != e.Seq {
				t.Fatalf("stream seqs %v; want seq %d next", got, e.Seq)
			}
			srv.CloseClientConnections()
		}
		cursor = page.Events[len(page.Events)-1].Seq
	}
}

// TestClientSubscribeSSEFieldRules: the client reads the stream by the SSE
// field rules, drops a repeated seq, reconnects from its cursor after
// subscriber_lagged and returns any other stream error.
func TestClientSubscribeSSEFieldRules(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/workspaces/ws/events/token" {
			_, _ = io.WriteString(w, `{"disabled":true}`)
			return
		}
		mu.Lock()
		queries = append(queries, r.URL.Query().Get("after"))
		n := len(queries)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			_, _ = io.WriteString(w, ": comment\nretry: 10\n\n"+
				"event: empty\n\n"+
				"foo: bar\nid: a1:5\nevent: event\ndata:{\"agent_id\":\"a1\",\n"+
				"data: \"seq\":5,\"kind\":\"item.completed\",\"payload\":{\"text\":\"a: b\"}}\n\n"+
				"event: event\r\ndata: {\"agent_id\":\"a1\",\"seq\":5}\r\n\r\n"+
				"event: event\ndata: {\"agent_id\":\"a1\",\"seq\":0,\"kind\":\"delta\"}\n\n"+
				"event: error\ndata: {\"error\":\"slow\",\"code\":\"subscriber_lagged\"}\n\n")
			return
		}
		// A saved event of kind error is an event, not the stream's error frame.
		_, _ = io.WriteString(w, "event: event\ndata: {\"agent_id\":\"a1\",\"seq\":6,\"kind\":\"error\"}\n\n"+
			"event: error\ndata: {\"error\":\"gone\",\"code\":\"cursor_expired\"}\n\n")
	}))
	t.Cleanup(srv.Close)
	s, err := newClient(srv, "ws", "").Subscribe(context.Background(), SubscribeRequest{Agents: []string{"a1"},
		After: map[string]int64{"a1": 4}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e, err := s.Next()
	if err != nil || e.Seq != 5 || e.Kind != "item.completed" || string(e.Payload) != `{"text":"a: b"}` {
		t.Fatalf("first = %+v, %v", e, err)
	}
	if e, err = s.Next(); err != nil || e.Kind != "delta" || e.Seq != 0 {
		t.Fatalf("second = %+v, %v; want the delta (the repeated seq 5 dropped)", e, err)
	}
	if e, err = s.Next(); err != nil || e.Seq != 6 || e.Kind != "error" {
		t.Fatalf("after lag = %+v, %v; want seq 6", e, err)
	}
	if _, err = s.Next(); code(err) != loomagent.CodeCursorExpired {
		t.Fatalf("stream error = %v; want cursor_expired", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(queries, []string{"a1:4", "a1:5"}) {
		t.Fatalf("after per connection = %q; want a1:4 then the last seq a1:5", queries)
	}
}
