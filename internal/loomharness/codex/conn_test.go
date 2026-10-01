package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// peer is the app-server end of an in-memory connection.
type peer struct {
	in  *bufio.Scanner // what Loom wrote
	out io.WriteCloser // what Loom reads
}

func newPair(t *testing.T, fallback Handler) (*Conn, *peer) {
	t.Helper()
	loomR, peerW := io.Pipe()
	peerR, loomW := io.Pipe()
	c := NewConn(loomR, loomW, fallback)
	t.Cleanup(func() { _ = peerW.Close(); _ = c.Close() })
	return c, &peer{in: bufio.NewScanner(peerR), out: peerW}
}

func (p *peer) read(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	if !p.in.Scan() {
		t.Fatalf("peer read: %v", p.in.Err())
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(p.in.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func (p *peer) write(t *testing.T, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if _, err := p.out.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

// TestConnConcurrentCalls answers 50 concurrent calls in reverse order; each
// caller gets its own result.
func TestConnConcurrentCalls(t *testing.T) {
	c, p := newPair(t, nil)
	const n = 50
	errs := make(chan error, n)
	for i := range n {
		go func() {
			var got struct{ N int }
			if err := c.Call(context.Background(), "echo", map[string]int{"n": i}, &got); err != nil || got.N != i {
				errs <- errors.Join(err, errors.New("wrong result"))
				return
			}
			errs <- nil
		}()
	}
	reqs := make([]map[string]json.RawMessage, n)
	for i := range reqs {
		reqs[i] = p.read(t)
	}
	for i := n - 1; i >= 0; i-- {
		var params struct{ N int }
		_ = json.Unmarshal(reqs[i]["params"], &params)
		p.write(t, map[string]any{"id": reqs[i]["id"], "result": map[string]int{"N": params.N}})
	}
	for range n {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

// TestConnServerRequests routes server requests by thread and answers each
// on the same connection with its own id; an unrouted one goes to the
// fallback, and with no fallback it is refused, never dropped.
func TestConnServerRequests(t *testing.T) {
	var mu sync.Mutex
	var fell []Message
	var c *Conn
	c, p := newPair(t, func(m Message) {
		mu.Lock()
		fell = append(fell, m)
		mu.Unlock()
		_ = c.Respond(m.ID, map[string]string{"by": "fallback"})
	})
	c.Route("t1", func(m Message) { _ = c.Respond(m.ID, map[string]string{"by": "t1:" + m.Method}) })

	p.write(t, map[string]any{"id": "s1", "method": "item/tool/requestUserInput", "params": map[string]string{"threadId": "t1"}})
	assertReply(t, p.read(t), `"s1"`, `{"by":"t1:item/tool/requestUserInput"}`)
	p.write(t, map[string]any{"id": 7, "method": "execCommandApproval", "params": map[string]string{"conversationId": "t2"}})
	assertReply(t, p.read(t), `7`, `{"by":"fallback"}`)
	mu.Lock()
	if len(fell) != 1 || fell[0].ThreadID != "t2" {
		t.Fatalf("fallback got %+v", fell)
	}
	mu.Unlock()

	_, bp := newPair(t, nil)
	bp.write(t, map[string]any{"id": 9, "method": "item/fileChange/requestApproval", "params": map[string]string{"threadId": "x"}})
	m := bp.read(t)
	if string(m["id"]) != "9" || m["error"] == nil {
		t.Fatalf("unrouted request with no fallback was not refused: %s", m)
	}
}

func assertReply(t *testing.T, m map[string]json.RawMessage, id, result string) {
	t.Helper()
	if string(m["id"]) != id || string(m["result"]) != result {
		t.Fatalf("reply id=%s result=%s, want id=%s result=%s", m["id"], m["result"], id, result)
	}
}

// TestConnErrorAndGap maps a JSON-RPC error, then ends the connection: the
// waiting call fails as unavailable and the routed thread and the fallback
// each get one gap.
func TestConnErrorAndGap(t *testing.T) {
	gaps := make(chan Message, 4)
	c, p := newPair(t, func(m Message) { gaps <- m })
	c.Route("t1", func(m Message) { gaps <- m })

	done := make(chan error, 1)
	go func() { done <- c.Call(context.Background(), "thread/read", nil, nil) }()
	p.write(t, map[string]any{"id": p.read(t)["id"], "error": map[string]any{"code": -32600, "message": "not materialized"}})
	var rpc *RPCError
	if err := <-done; !errors.As(err, &rpc) || rpc.Code != -32600 {
		t.Fatalf("want RPCError -32600, got %v", err)
	}

	go func() { done <- c.Call(context.Background(), "turn/start", nil, nil) }()
	p.read(t)
	_ = p.out.Close()
	if err := <-done; !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
	got := map[string]bool{}
	for range 2 {
		select {
		case m := <-gaps:
			got[m.ThreadID] = m.Gap
		case <-time.After(5 * time.Second):
			t.Fatal("no gap")
		}
	}
	if !got["t1"] || !got[""] {
		t.Fatalf("gaps: %v", got)
	}
	if err := c.Call(context.Background(), "x", nil, nil); !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("call after end: %v", err)
	}
}
