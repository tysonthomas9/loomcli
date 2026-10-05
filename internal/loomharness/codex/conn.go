// Package codex is the codex harness adapter. This file is its JSON-RPC 2.0
// client for one `codex app-server` stdio connection; supervisor.go runs one
// app-server per codex root.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/codex/protocol"
)

// Message is a notification or server request from codex, or a gap.
type Message struct {
	ID       protocol.RequestId // set on a server request: answer it with Respond or RespondError
	Method   string
	Params   json.RawMessage
	ThreadID string // params.threadId (or conversationId, or thread.id); empty when none
	// Gap is set once, when the connection ends: this thread's running turn
	// and its unanswered server requests are lost.
	Gap bool
	c   *Conn // the connection it came on, where a server request is answered
}

// Respond answers this server request on the connection it came on.
func (m Message) Respond(result any) error { return m.c.Respond(m.ID, result) }

// RespondError refuses this server request on the connection it came on.
func (m Message) RespondError(code int64, message string) error {
	return m.c.RespondError(m.ID, code, message)
}

// Handler receives messages on the connection's one reader, in arrival
// order. It must not block or make Calls; it hands work to its own goroutine.
type Handler func(Message)

// RPCError is a JSON-RPC error codex returned for a call.
type RPCError protocol.JSONRPCErrorError

func (e *RPCError) Error() string { return fmt.Sprintf("codex: %s (%d)", e.Message, e.Code) }

// Conn is one app-server connection: one reader matches responses to calls by
// id and routes notifications and server requests by thread, and answers go
// back on the same connection.
type Conn struct {
	w        io.WriteCloser
	wmu      sync.Mutex
	fallback Handler

	mu      sync.Mutex
	next    int64
	pending map[string]chan reply // by request id
	routes  map[string]route
	routeN  uint64
	err     error // set when the connection ended
	done    chan struct{}
}

// NewConn starts the reader on r. fallback receives messages for threads
// with no route, plus a Gap with no ThreadID when the connection ends; if it
// is nil, unrouted server requests are answered with an error, never dropped.
func NewConn(r io.Reader, w io.WriteCloser, fallback Handler) *Conn {
	c := &Conn{w: w, fallback: fallback,
		pending: map[string]chan reply{}, routes: map[string]route{}, done: make(chan struct{})}
	go c.read(r)
	return c
}

// Call sends a request and waits for its response; result may be nil.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.next++
	id := strconv.FormatInt(c.next, 10)
	ch := make(chan reply, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()

	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := c.send(protocol.JSONRPCRequest{Id: protocol.RequestId(id), Method: method, Params: p}); err != nil {
		return err
	}
	select {
	case r := <-ch:
		switch {
		case r.lost:
			return c.Err()
		case r.err != nil:
			e := RPCError(*r.err)
			return &e
		case result == nil:
			return nil
		}
		return json.Unmarshal(r.result, result)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Notify sends a notification; nil params are left out.
func (c *Conn) Notify(method string, params any) error {
	n := protocol.JSONRPCNotification{Method: method}
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return err
		}
		n.Params = p
	}
	return c.send(n)
}

// Respond answers a server request with its own id.
func (c *Conn) Respond(id protocol.RequestId, result any) error {
	r, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return c.send(protocol.JSONRPCResponse{Id: id, Result: r})
}

// RespondError refuses a server request.
func (c *Conn) RespondError(id protocol.RequestId, code int64, message string) error {
	return c.send(protocol.JSONRPCError{Id: id, Error: protocol.JSONRPCErrorError{Code: code, Message: message}})
}

// Route sends the messages for threadID to h until the returned func is
// called. A later Route for the same thread replaces h. On a connection that
// has already ended, h gets its Gap at once, before Route returns.
func (c *Conn) Route(threadID string, h Handler) (unroute func()) {
	c.mu.Lock()
	ended := c.err != nil
	c.routeN++
	n := c.routeN
	if !ended {
		c.routes[threadID] = route{h: h, n: n}
	}
	c.mu.Unlock()
	if ended {
		h(Message{ThreadID: threadID, Gap: true})
	}
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.routes[threadID].n == n {
			delete(c.routes, threadID)
		}
	}
}

// Done is closed when the connection ends; Err then says why.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err is nil while the connection is up.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close closes the write side; a codex app-server exits when stdin closes.
func (c *Conn) Close() error { return c.w.Close() }

func (c *Conn) send(v any) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.Err(); err != nil {
		return fmt.Errorf("%w: %w", loomharness.ErrNotSent, err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if n, err := c.w.Write(append(b, '\n')); err != nil && n == 0 {
		return fmt.Errorf("codex: write: %w: %w: %w", loomharness.ErrNotSent, loomharness.ErrUnavailable, err) // nothing written
	} else if err != nil {
		return fmt.Errorf("codex: write: %w: %w", loomharness.ErrUnavailable, err)
	}
	return nil
}

type route struct {
	h Handler
	n uint64 // tells a replaced route's unroute apart
}

// reply is a call's outcome: a result, an error, or the connection's end.
type reply struct {
	result json.RawMessage
	err    *protocol.JSONRPCErrorError
	lost   bool
}

// inbound is any JSON-RPC message codex sends.
type inbound struct {
	ID     protocol.RequestId          `json:"id"`
	Method string                      `json:"method"`
	Params json.RawMessage             `json:"params"`
	Result json.RawMessage             `json:"result"`
	Error  *protocol.JSONRPCErrorError `json:"error"`
}

func (c *Conn) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for sc.Scan() {
		var m inbound
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue // not JSON-RPC
		}
		if m.Method == "" {
			c.resolve(m)
			continue
		}
		msg := Message{ID: m.ID, Method: m.Method, Params: m.Params, ThreadID: threadOf(m.Params), c: c}
		if h := c.handler(msg.ThreadID); h != nil {
			h(msg)
		} else if msg.ID != nil {
			_ = c.RespondError(msg.ID, -32601, "loom: no handler for "+msg.Method)
		}
	}
	c.end(sc.Err())
}

// resolve hands a response to the call waiting on its id.
func (c *Conn) resolve(m inbound) {
	var id string
	if json.Unmarshal(m.ID, &id) != nil {
		id = string(m.ID) // a numeric id
	}
	c.mu.Lock()
	ch := c.pending[id]
	c.mu.Unlock()
	select {
	case ch <- reply{result: m.Result, err: m.Error}: // buffered; only this reader sends
	default: // nil: a call that already gave up; full: a duplicate response
	}
}

func (c *Conn) handler(threadID string) Handler {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.routes[threadID]; ok && threadID != "" {
		return r.h
	}
	return c.fallback
}

// end fails every waiting call and tells every routed thread, then the
// fallback, about the gap.
func (c *Conn) end(err error) {
	if err == nil {
		err = io.EOF
	}
	c.mu.Lock()
	c.err = fmt.Errorf("codex: connection closed: %w: %w", loomharness.ErrUnavailable, err)
	for _, ch := range c.pending {
		select {
		case ch <- reply{lost: true}:
		default: // already holds its response
		}
	}
	routes := c.routes
	c.routes = map[string]route{}
	c.mu.Unlock()
	close(c.done)
	for thread, r := range routes {
		r.h(Message{ThreadID: thread, Gap: true})
	}
	if c.fallback != nil {
		c.fallback(Message{Gap: true})
	}
}

// threadOf finds the thread a message belongs to.
func threadOf(params json.RawMessage) string {
	var p struct {
		ThreadID       string `json:"threadId"`
		ConversationID string `json:"conversationId"`
		Thread         struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(params, &p) != nil {
		return ""
	}
	switch {
	case p.ThreadID != "":
		return p.ThreadID
	case p.ConversationID != "":
		return p.ConversationID
	}
	return p.Thread.ID
}
