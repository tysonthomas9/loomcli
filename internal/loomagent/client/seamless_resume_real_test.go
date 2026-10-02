package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/testutil/realloom"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentsv1"
)

// TestSeamlessResume/opencode/serve_restart runs a real `loom serve` process
// on the real OpenCode build, driven by the AFT fake-model fixture, and stops
// it (SIGTERM, then SIGKILL) while a turn runs and a message waits. The turn
// ends while serve is down; serve restarts on the same data, and the same
// agent and native session take the waiting message exactly once, with the
// full history, within 5 s. An SSE stream reconnected from its last cursor
// continues with no missing or repeated event, matching ListEvents, with no
// Attention or error. LOOM_REAL_OPENCODE=1 runs it.
func TestSeamlessResume(t *testing.T) {
	t.Run("opencode/serve_restart", func(t *testing.T) {
		realloom.Skip(t)
		t.Run("sigterm", func(t *testing.T) { serveRestart(t, syscall.SIGTERM) })
		t.Run("sigkill", func(t *testing.T) { serveRestart(t, syscall.SIGKILL) })
	})
}

func serveRestart(t *testing.T, sig syscall.Signal) {
	ctx := context.Background()
	fixture := startFakeModel(t)
	model := newModelGate(t, fixture)
	sbx := realloom.NewSandbox(t, model.URL)
	if resp, err := http.Post(fixture+"/__script", "application/json",
		strings.NewReader(`{"steps":[{"text":"reply-one"},{"text":"reply-two"},{"text":"reply-three"}]}`)); err != nil {
		t.Fatal(err)
	} else {
		_ = resp.Body.Close()
	}
	serve := sbx.StartServer(t, sbx.BuildLoom(t))
	c := New(Config{BaseURL: serve.URL + "/", Workspace: realloom.Workspace, HTTP: &http.Client{}})
	a, err := c.Create(ctx, "r1", agentsv1.CreateBody{Preset: "pr-review-interactive", Name: "rev", Repo: sbx.Repo,
		BaseRef: sbx.Head, Overrides: agentsv1.Overrides{Harness: "opencode"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	idle := func() bool {
		got, err := c.Get(ctx, a.AgentID)
		return err == nil && got.State == loomagent.StateIdle && len(got.WaitingMessages) == 0
	}
	eventually(t, "idle after create", idle)

	// The page's stream, from the agent's first event, until serve goes away.
	var seen []agentsv1.Event
	first, err := c.Subscribe(ctx, SubscribeRequest{Agents: []string{a.AgentID}, After: map[string]int64{a.AgentID: 0}})
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		for e, err := first.Next(); err == nil; e, err = first.Next() {
			if e.Seq > 0 {
				seen = append(seen, e)
			}
		}
	}()
	send := func(text string) {
		if _, err := c.Send(ctx, text, a.AgentID, text); err != nil {
			t.Fatalf("Send %s: %v", text, err)
		}
	}
	send("msg-one")
	eventually(t, "idle after turn one", func() bool { return idle() && model.count(t, "msg-one") == 1 })

	// Turn two runs (its model reply held) and msg-three waits behind it.
	model.armed.Store(true)
	send("msg-two")
	wait(t, model.held, "turn two's model request")
	send("msg-three")
	before, err := c.Get(ctx, a.AgentID)
	if err != nil || before.State != loomagent.StateActive || len(before.WaitingMessages) != 1 {
		t.Fatalf("before restart: state %q, waiting %d, %v; want active with one waiting", before.State, len(before.WaitingMessages), err)
	}

	// Stop serve; the turn ends in OpenCode while serve is down; restart serve.
	serve.Stop(sig)
	<-firstDone
	_ = first.Close()
	nativeBefore := nativeSession(t, sbx.Dir, a.AgentID)
	close(model.gate)
	wait(t, model.done, "turn two's reply")
	restarted := time.Now()
	serve.Start()
	eventually(t, "the waiting message reaching the model", func() bool { return model.count(t, "msg-three") == 1 })
	if d := time.Since(restarted); d > 5*time.Second {
		t.Errorf("the waiting message's turn started %s after the restart; want under 5s", d)
	}

	// The page reconnects from its last cursor and reads on to idle.
	var cursor int64
	if len(seen) > 0 {
		cursor = seen[len(seen)-1].Seq
	}
	sctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	second, err := c.Subscribe(sctx, SubscribeRequest{Agents: []string{a.AgentID}, After: map[string]int64{a.AgentID: cursor}})
	if err != nil {
		t.Fatal(err)
	}
	for three := false; ; {
		e, err := second.Next()
		if err != nil {
			t.Fatalf("stream after restart: %v", err)
		}
		if e.Seq == 0 {
			continue
		}
		seen = append(seen, e)
		three = three || e.Kind == "message.delivered" && strings.Contains(string(e.Payload), "msg-three")
		if three && e.Kind == loomagent.EventIdle {
			break
		}
	}
	_ = second.Close()
	eventually(t, "idle after turn three", idle)

	// Exactly once, nothing resent, the full history in the same session.
	for _, m := range []string{"msg-one", "msg-two", "msg-three"} {
		if n := model.count(t, m); n != 1 {
			t.Errorf("the model got %s as the new message %d times; want 1", m, n)
		}
	}
	turns := model.turns(t)
	if last := turns[len(turns)-1]; !strings.Contains(last, "reply-one") || !strings.Contains(last, "reply-two") ||
		!strings.Contains(last, "msg-one") || !strings.Contains(last, "msg-two") {
		t.Errorf("turn three's request lacks the earlier history: %s", last)
	}
	if after, err := c.Get(ctx, a.AgentID); err != nil || after.AttentionReason != nil {
		t.Errorf("after restart: attention %v, %v", after.AttentionReason, err)
	}

	// The streamed events: seqs 1..N, each EventID once, no Attention or
	// error, each message delivered once, and the same as ListEvents.
	ids, delivered := map[string]bool{}, map[string]int{}
	for i, e := range seen {
		if e.Seq != int64(i+1) || ids[e.EventID] {
			t.Errorf("streamed event %d: seq %d, event id %s repeated %v", i, e.Seq, e.EventID, ids[e.EventID])
		}
		ids[e.EventID] = true
		if e.Kind == loomagent.EventAttentionRaised || strings.Contains(e.Kind, "error") || strings.Contains(string(e.Payload), `"error"`) {
			t.Errorf("event %d %s: %s", e.Seq, e.Kind, e.Payload)
		}
		for _, m := range []string{"msg-one", "msg-two", "msg-three"} {
			if e.Kind == "message.delivered" && strings.Contains(string(e.Payload), m) {
				delivered[m]++
			}
		}
	}
	for _, m := range []string{"msg-one", "msg-two", "msg-three"} {
		if delivered[m] != 1 {
			t.Errorf("%s delivered %d times on the stream; want 1", m, delivered[m])
		}
	}
	page, err := c.ListEvents(ctx, loomstore.EventQuery{AgentID: a.AgentID, Limit: len(seen)})
	if err != nil || len(page.Events) != len(seen) {
		t.Fatalf("ListEvents: %d events, %v; want %d", len(page.Events), err, len(seen))
	}
	for i, e := range page.Events {
		if e.EventID != seen[i].EventID {
			t.Errorf("ListEvents event %d is %s; the stream had %s", e.Seq, e.EventID, seen[i].EventID)
		}
	}
	serve.Stop(syscall.SIGTERM)
	if got := nativeSession(t, sbx.Dir, a.AgentID); got != nativeBefore || got == "" {
		t.Errorf("native session %q after restart; want %q", got, nativeBefore)
	}
	t.Logf("%s: the stream reconnected at seq %d and read %d events, no gap", sig, cursor, len(seen))
}

func wait(t *testing.T, ch chan struct{}, what string) {
	select {
	case <-ch:
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// nativeSession reads the agent's native session from the registry while
// serve is stopped.
func nativeSession(t *testing.T, sbx, agentID string) string {
	t.Helper()
	st, err := loomstore.Open(context.Background(), filepath.Join(sbx, "loom/agents.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	a, err := st.GetAgent(context.Background(), agentID)
	if err != nil || a.HarnessSessionID == nil {
		t.Fatalf("agent %s: %v", agentID, err)
	}
	return *a.HarnessSessionID
}

// startFakeModel runs the AFT fake-model fixture on a free port and returns
// its base URL.
func startFakeModel(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the fake-model fixture: %v", err)
	}
	cmd := exec.Command(node, "../../../tests/aft/fixtures/fake-model/server.mjs")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "FAKE_MODEL_PORT=0"}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	port, ok := strings.CutPrefix(strings.TrimSpace(line), "fake-model listening ")
	if err != nil || !ok {
		t.Fatalf("fake-model start: %q, %v", line, err)
	}
	return "http://127.0.0.1:" + port
}

// modelGate passes OpenCode's model requests to the fixture. Once armed, it
// holds the next turn's request (closing held) until gate is closed, and
// closes done when that reply has been sent.
type modelGate struct {
	*httptest.Server
	fixture          string
	armed            atomic.Bool
	held, gate, done chan struct{}
}

func newModelGate(t *testing.T, fixture string) *modelGate {
	m := &modelGate{fixture: fixture, held: make(chan struct{}), gate: make(chan struct{}), done: make(chan struct{})}
	target, _ := url.Parse(fixture)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if !bytes.Contains(body, []byte("You are a title generator")) && m.armed.CompareAndSwap(true, false) {
			close(m.held)
			<-m.gate
			defer close(m.done)
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(m.Close)
	return m
}

// turns returns the fixture's recorded turn requests (title requests left
// out), each as its JSON body.
func (m *modelGate) turns(t *testing.T) []string {
	resp, err := http.Get(m.fixture + "/__requests")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var log struct{ Requests []json.RawMessage }
	if err := json.NewDecoder(resp.Body).Decode(&log); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range log.Requests {
		if !bytes.Contains(r, []byte("You are a title generator")) {
			out = append(out, string(r))
		}
	}
	return out
}

// count is how many turn requests had a newest message containing s.
func (m *modelGate) count(t *testing.T, s string) int {
	n := 0
	for _, r := range m.turns(t) {
		var req struct{ Messages []json.RawMessage }
		if json.Unmarshal([]byte(r), &req) == nil && len(req.Messages) > 0 &&
			strings.Contains(string(req.Messages[len(req.Messages)-1]), s) {
			n++
		}
	}
	return n
}
