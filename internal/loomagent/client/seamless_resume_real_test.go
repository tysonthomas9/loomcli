package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/netutil"
	"github.com/tysonthomas9/loomcli/internal/store"
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
		if os.Getenv("LOOM_REAL_OPENCODE") != "1" {
			t.Skip("set LOOM_REAL_OPENCODE=1 to run against the real OpenCode build")
		}
		dir, err := os.MkdirTemp("/tmp", "loom-bin-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		loom := filepath.Join(dir, "loom")
		if out, err := exec.Command("go", "build", "-o", loom, "../../../cmd/loom").CombinedOutput(); err != nil {
			t.Fatalf("build loom: %v %s", err, out)
		}
		t.Run("sigterm", func(t *testing.T) { serveRestart(t, loom, syscall.SIGTERM) })
		t.Run("sigkill", func(t *testing.T) { serveRestart(t, loom, syscall.SIGKILL) })
	})
}

func serveRestart(t *testing.T, loom string, sig syscall.Signal) {
	bin := os.Getenv("LOOM_OPENCODE_BIN")
	if bin == "" {
		home, _ := os.UserHomeDir()
		bin = filepath.Join(home, ".loom/harness/opencode/2.0.19/opencode")
	}
	sbx := realSandbox(t)
	model := newHoldingModel(t, startFakeModel(t))
	model.script(t, "reply-one", "reply-two", "reply-three")
	writeFile(t, filepath.Join(sbx, "config/opencode/opencode.json"), fmt.Sprintf(`{"provider":{"fake":{"name":"Fake",
		"npm":"@ai-sdk/openai-compatible","options":{"baseURL":%q,"apiKey":"x"},
		"models":{"m":{"name":"M","limit":{"context":100000,"output":4000}}}}},"model":"fake/m"}`, model.URL+"/v1"))
	repo := filepath.Join(sbx, "repo")
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.name=t", "-c", "user.email=t@t",
		"commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}

	// An owned fleet-db with the workspace, and serve's environment.
	ctx := context.Background()
	fleet, err := bootstrap.StartEmbedded(ctx, filepath.Join(sbx, "fleet"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fleet.Stop() })
	fc, err := fleetdb.New(fleetdb.Config{BaseURL: fleet.URL(), Actor: "loom-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fc.Close() }()
	if _, err := fc.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "WS"}); err != nil {
		t.Fatal(err)
	}
	_, port, err := netutil.PickFreeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + sbx + "/home", "TMPDIR=" + sbx + "/tmp/",
		"XDG_DATA_HOME=" + sbx + "/data", "XDG_CONFIG_HOME=" + sbx + "/config", "XDG_STATE_HOME=" + sbx + "/state",
		"XDG_CACHE_HOME=" + sbx + "/cache", "OPENCODE_DISABLE_MODELS_FETCH=1", "LOOM_OPENCODE_BIN=" + bin,
		"LOOM_CONFIG_DIR=" + sbx + "/loom", "LOOM_WORKSPACE=WS", "LOOM_FLEET_DB_URL=" + fleet.URL(),
		"LOOM_FLEET_DB_ACTOR=loom-test", "LOOM_DRIVER_EXECUTOR=0", "LOOM_ISSUE_BRIDGE_DISABLED=1", "LOOM_DISABLE_H2C=1"}
	var serve *exec.Cmd
	var logs bytes.Buffer
	start := func() {
		serve = exec.Command(loom, "serve", "--no-daemon", "--bind", "127.0.0.1", "--port", strconv.Itoa(port))
		serve.Dir, serve.Env, serve.Stdout, serve.Stderr = repo, env, &logs, &logs
		if err := serve.Start(); err != nil {
			t.Fatal(err)
		}
		for end := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if resp, err := http.Get(base + "/health"); err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return
				}
			}
			if time.Now().After(end) {
				t.Fatalf("loom serve never became healthy:\n%s", logs.String())
			}
		}
	}
	stop := func(sig syscall.Signal) {
		_ = serve.Process.Signal(sig)
		_ = serve.Wait()
	}
	start()
	t.Cleanup(func() {
		if serve.ProcessState == nil {
			stop(syscall.SIGTERM)
		}
		if t.Failed() {
			t.Logf("loom serve output:\n%s", logs.String())
		}
	})
	c := New(Config{BaseURL: base + "/", Workspace: "WS", HTTP: &http.Client{}})

	a, err := c.Create(ctx, "r1", agentsv1.CreateBody{Preset: "pr-review-interactive", Name: "rev", Repo: repo,
		BaseRef: strings.TrimSpace(string(head)), Overrides: agentsv1.Overrides{Harness: "opencode"}})
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

	if _, err := c.Send(ctx, "s1", a.AgentID, "msg-one"); err != nil {
		t.Fatalf("Send one: %v", err)
	}
	eventually(t, "idle after turn one", func() bool { return idle() && model.count("msg-one") == 1 })

	// Turn two runs (its model reply held) and msg-three waits behind it.
	model.hold()
	if _, err := c.Send(ctx, "s2", a.AgentID, "msg-two"); err != nil {
		t.Fatalf("Send two: %v", err)
	}
	model.waitHeld(t)
	if _, err := c.Send(ctx, "s3", a.AgentID, "msg-three"); err != nil {
		t.Fatalf("Send three: %v", err)
	}
	before, err := c.Get(ctx, a.AgentID)
	if err != nil || before.State != loomagent.StateActive || len(before.WaitingMessages) != 1 {
		t.Fatalf("before restart: state %q, waiting %d, %v; want active with one waiting", before.State, len(before.WaitingMessages), err)
	}

	// Stop serve; the turn ends in OpenCode while serve is down; restart serve.
	stop(sig)
	<-firstDone
	_ = first.Close()
	nativeBefore := nativeSession(t, sbx, a.AgentID)
	model.release(t)
	restarted := time.Now()
	start()

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
	if d := model.firstSeen("msg-three").Sub(restarted); d > 5*time.Second {
		t.Errorf("the waiting message's turn started %s after the restart; want under 5s", d)
	}
	eventually(t, "idle after turn three", idle)

	// Exactly once, nothing resent, the full history in the same session.
	for _, m := range []string{"msg-one", "msg-two", "msg-three"} {
		if n := model.count(m); n != 1 {
			t.Errorf("the model got %s as the new message %d times; want 1", m, n)
		}
	}
	if last := model.last(); !strings.Contains(last, "reply-one") || !strings.Contains(last, "reply-two") ||
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
	stop(syscall.SIGTERM)
	if got := nativeSession(t, sbx, a.AgentID); got != nativeBefore || got == "" {
		t.Errorf("native session %q after restart; want %q", got, nativeBefore)
	}
	t.Logf("%s: the waiting message reached the model %s after the restart; the stream reconnected at seq %d and read %d events, no gap",
		sig, model.firstSeen("msg-three").Sub(restarted), cursor, len(seen))
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

// holdingModel sits in front of the fixture, records each turn request with
// its arrival time, and on hold keeps the next turn's reply until release.
type holdingModel struct {
	*httptest.Server
	fixture string
	mu      sync.Mutex
	reqs    []turnRequest
	held    chan struct{} // closed when the held request arrives
	gate    chan struct{} // closed on release
	done    chan struct{} // closed when the held reply has been sent
}

type turnRequest struct {
	at        time.Time
	body, msg string // msg: the newest message
}

func newHoldingModel(t *testing.T, fixture string) *holdingModel {
	m := &holdingModel{fixture: fixture}
	target, _ := url.Parse(fixture)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var req struct{ Messages []json.RawMessage }
		_ = json.Unmarshal(body, &req)
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") || bytes.Contains(body, []byte("You are a title generator")) ||
			len(req.Messages) == 0 {
			proxy.ServeHTTP(w, r)
			return
		}
		m.mu.Lock()
		m.reqs = append(m.reqs, turnRequest{time.Now(), string(body), string(req.Messages[len(req.Messages)-1])})
		held, gate, done := m.held, m.gate, m.done
		m.held = nil
		m.mu.Unlock()
		if held != nil {
			close(held)
			<-gate
			defer close(done)
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(m.Close)
	return m
}

func (m *holdingModel) script(t *testing.T, texts ...string) {
	steps := []map[string]string{}
	for _, s := range texts {
		steps = append(steps, map[string]string{"text": s})
	}
	b, _ := json.Marshal(map[string]any{"steps": steps})
	resp, err := http.Post(m.fixture+"/__script", "application/json", bytes.NewReader(b))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("script the fake model: %v", err)
	}
	_ = resp.Body.Close()
}

func (m *holdingModel) hold() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.held, m.gate, m.done = make(chan struct{}), make(chan struct{}), make(chan struct{})
}

func (m *holdingModel) waitHeld(t *testing.T) {
	m.mu.Lock()
	held := m.held
	m.mu.Unlock()
	select {
	case <-held:
	case <-time.After(30 * time.Second):
		t.Fatal("the held turn never reached the model")
	}
}

// release sends the held reply and waits until it has been sent.
func (m *holdingModel) release(t *testing.T) {
	close(m.gate)
	select {
	case <-m.done:
	case <-time.After(30 * time.Second):
		t.Fatal("the held reply was never sent")
	}
}

// count is how many turn requests had a newest message containing s.
func (m *holdingModel) count(s string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.reqs {
		if strings.Contains(r.msg, s) {
			n++
		}
	}
	return n
}

func (m *holdingModel) firstSeen(s string) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.reqs {
		if strings.Contains(r.msg, s) {
			return r.at
		}
	}
	return time.Time{}
}

func (m *holdingModel) last() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reqs[len(m.reqs)-1].body
}
