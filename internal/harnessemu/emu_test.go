package harnessemu_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/harnessemu"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/opencode"
)

// emu starts an emulator on state and returns the adapter's client for it.
func emu(t *testing.T, state, scenarios string) (*opencode.Client, func()) {
	c, _, stop := emuURL(t, state, scenarios)
	return c, stop
}

func emuURL(t *testing.T, state, scenarios string) (*opencode.Client, string, func()) {
	t.Helper()
	s, err := harnessemu.New(state, scenarios, "pw")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	stop := func() { s.Close(); srv.Close() }
	t.Cleanup(stop)
	return opencode.NewClient(srv.URL, "pw"), srv.URL, stop
}

func scenarios(t *testing.T, m map[string][]harnessemu.Turn) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "scenarios.json")
	b, _ := json.Marshal(m)
	if err := os.WriteFile(f, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

// until reads feed events until ok matches one, and returns all read.
func until(t *testing.T, f loomharness.Feed, ok func(loomharness.Event) bool) []loomharness.Event {
	t.Helper()
	var got []loomharness.Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-f.Events():
			got = append(got, e)
			if ok(e) {
				return got
			}
		case <-timeout:
			t.Fatalf("no matching event; got %+v", got)
		}
	}
}

func completed(e loomharness.Event) bool { return e.Type == loomharness.EventTurnCompleted }

func history(t *testing.T, s *opencode.Session, limit int) []loomharness.Event {
	t.Helper()
	var out []loomharness.Event
	for after := ""; ; {
		p, err := s.Messages(context.Background(), after, limit)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p.Events...)
		if p.Next == "" {
			return out
		}
		after = p.Next
	}
}

func TestEmulatorScenarioTurns(t *testing.T) {
	ctx := context.Background()
	sc := scenarios(t, map[string][]harnessemu.Turn{"agent-1": {
		{Reasoning: "think", Text: "hello there world", Child: true, Tokens: harnessemu.Tokens{Input: 10, Output: 3}, Cost: 0.5},
		{Fail: "model exploded"},
		{Hold: true, Text: "slow"},
	}})
	c, url, _ := emuURL(t, filepath.Join(t.TempDir(), "state.json"), sc)
	f, err := c.Feed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ref, err := c.Open(ctx, loomharness.OpenSpec{Key: "agent-1", Dir: t.TempDir(), Metadata: map[string]string{"agent_id": "agent-1"}})
	if err != nil {
		t.Fatal(err)
	}
	s := c.Session(ref)
	k1 := opencode.PromptID("agent-1", "r1")

	// Turn 1 streams, starts a child and reports usage. The prompt text
	// ("FAIL") never selects behavior.
	if err := s.Prompt(ctx, loomharness.Input{Key: k1, Text: "FAIL"}); err != nil {
		t.Fatal(err)
	}
	live := until(t, f, completed)
	var text string
	var usage *loomharness.Usage
	child := false
	for _, e := range live {
		switch e.Type {
		case loomharness.EventDelta:
			if e.ItemKind == "message" {
				text += e.Text
			}
		case loomharness.EventUsage:
			usage = &e.Usage
		case loomharness.EventSubagentStarted:
			child = e.Session.NativeID == ref.NativeID
		}
	}
	if text != "hello there world" || usage == nil || usage.InputTokens != 10 || usage.CostUSD != 0.5 || !child || live[len(live)-1].StopReason != "completed" {
		t.Fatalf("turn 1: text %q usage %+v child %v events %+v", text, usage, child, live)
	}
	var list struct {
		Data []struct{ ID, ParentID string } `json:"data"`
	}
	if err := getJSON(url+"/api/session", &list); err != nil || len(list.Data) != 2 {
		t.Fatalf("session list %+v, %v", list, err)
	}

	// History matches the live feed, paged or not.
	var liveTurn []loomharness.Event
	for _, e := range live {
		if e.Type != loomharness.EventDelta && e.Type != loomharness.EventItemStarted && e.Type != loomharness.EventSubagentStarted {
			liveTurn = append(liveTurn, e)
		}
	}
	whole, paged := history(t, s, 100), history(t, s, 1)
	if len(whole) != len(liveTurn) || len(paged) != len(whole) {
		t.Fatalf("history %d, paged %d, live %d: %+v vs %+v", len(whole), len(paged), len(liveTurn), whole, liveTurn)
	}
	for i := range whole {
		if whole[i].Type != liveTurn[i].Type || whole[i].ItemID != liveTurn[i].ItemID || whole[i].TurnID != liveTurn[i].TurnID ||
			paged[i].Type != whole[i].Type || paged[i].ItemID != whole[i].ItemID {
			t.Fatalf("event %d: history %+v, paged %+v, live %+v", i, whole[i], paged[i], liveTurn[i])
		}
	}

	// A repeat prompt id is kept once; HasInput finds only sent ids.
	if err := s.Prompt(ctx, loomharness.Input{Key: k1, Text: "again"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.HasInput(ctx, k1); got != loomharness.LandedFound {
		t.Fatalf("HasInput(sent) = %v", got)
	}
	if got, _ := s.HasInput(ctx, opencode.PromptID("agent-1", "nope")); got != loomharness.LandedNotFound {
		t.Fatalf("HasInput(unsent) = %v", got)
	}

	// Turn 2 fails with no usage.
	if err := s.Prompt(ctx, loomharness.Input{Key: opencode.PromptID("agent-1", "r2"), Text: "x"}); err != nil {
		t.Fatal(err)
	}
	for _, e := range until(t, f, completed) {
		if e.Type == loomharness.EventUsage || (completed(e) && e.StopReason != "failed") {
			t.Fatalf("failed turn: %+v", e)
		}
	}

	// Turn 3 holds until interrupted; a late interrupt does nothing.
	if err := s.Prompt(ctx, loomharness.Input{Key: opencode.PromptID("agent-1", "r3"), Text: "x"}); err != nil {
		t.Fatal(err)
	}
	until(t, f, func(e loomharness.Event) bool { return e.Type == loomharness.EventItemCompleted })
	if st, _ := s.Status(ctx); !st.Running {
		t.Fatalf("Status = %+v; want running", st)
	}
	if ok, err := s.Interrupt(ctx); err != nil || !ok {
		t.Fatalf("Interrupt = %v, %v", ok, err)
	}
	if e := until(t, f, completed); e[len(e)-1].StopReason != "cancelled" {
		t.Fatalf("interrupted turn = %+v", e)
	}
	if st, _ := s.Status(ctx); st.Running || !st.LastTurnInterrupt {
		t.Fatalf("Status = %+v; want idle, interrupted", st)
	}
	if ok, err := s.Interrupt(ctx); err != nil || ok {
		t.Fatalf("late Interrupt = %v, %v", ok, err)
	}

	// No scripted turn left: the reply echoes the prompt.
	if err := s.Prompt(ctx, loomharness.Input{Key: opencode.PromptID("agent-1", "r4"), Text: "echo me"}); err != nil {
		t.Fatal(err)
	}
	text = ""
	for _, e := range until(t, f, completed) {
		if e.Type == loomharness.EventDelta {
			text += e.Text
		}
	}
	if text != "echo me" {
		t.Fatalf("echo = %q", text)
	}
}

func getJSON(url string, out any) error {
	req, _ := http.NewRequest("GET", url, nil)
	req.SetBasicAuth("opencode", "pw")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

func TestEmulatorRestartResumesRunningTurn(t *testing.T) {
	ctx := context.Background()
	state := filepath.Join(t.TempDir(), "state.json")
	sc := scenarios(t, map[string][]harnessemu.Turn{"agent-2": {{Text: "a b c d e f g h", DelayMS: 50}}})
	c, stop := emu(t, state, sc)
	ref, err := c.Open(ctx, loomharness.OpenSpec{Key: "agent-2", Dir: t.TempDir(), Metadata: map[string]string{"agent_id": "agent-2"}})
	if err != nil {
		t.Fatal(err)
	}
	key := opencode.PromptID("agent-2", "r1")
	if err := c.Session(ref).Prompt(ctx, loomharness.Input{Key: key, Text: "go"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	stop() // a graceful stop mid-turn

	c, _ = emu(t, state, sc)
	s := c.Session(ref)
	deadline := time.Now().Add(5 * time.Second)
	for st, _ := s.Status(ctx); st.Running; st, _ = s.Status(ctx) {
		if time.Now().After(deadline) {
			t.Fatal("resumed turn never finished")
		}
		time.Sleep(20 * time.Millisecond)
	}
	var resumed, done int
	for _, e := range history(t, s, 100) {
		switch {
		case e.Type == loomharness.EventTurnResumed && e.Text == harnessemu.RestartNotice:
			resumed++
		case completed(e) && e.StopReason == "completed":
			done++
		}
	}
	if resumed != 1 || done != 1 {
		t.Fatalf("restart: %d resumed, %d completed", resumed, done)
	}
	if got, _ := s.HasInput(ctx, key); got != loomharness.LandedFound {
		t.Fatalf("HasInput after restart = %v", got)
	}
}

func TestEmulatorChildSessionQuery(t *testing.T) {
	ctx := context.Background()
	child := []harnessemu.Turn{{Text: "x", Child: true}}
	sc := scenarios(t, map[string][]harnessemu.Turn{"p1": child, "p2": append(child, child...)})
	c, url, _ := emuURL(t, filepath.Join(t.TempDir(), "state.json"), sc)
	refs := map[string]loomharness.NativeRef{}
	for _, key := range []string{"p1", "p2", "lone"} {
		ref, err := c.Open(ctx, loomharness.OpenSpec{Key: key, Dir: t.TempDir(), Metadata: map[string]string{"agent_id": key}})
		if err != nil {
			t.Fatal(err)
		}
		refs[key] = ref
	}
	for i, key := range []string{"p1", "p2", "p2"} {
		s := c.Session(refs[key])
		if err := s.Prompt(ctx, loomharness.Input{Key: opencode.PromptID(key, string(rune('a'+i))), Text: "go"}); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			if st, _ := s.Status(ctx); !st.Running {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("turn never finished")
			}
		}
	}
	list := func(q string) map[string]string {
		var page struct {
			Data []struct{ ID, ParentID string } `json:"data"`
		}
		if err := getJSON(url+"/api/session"+q, &page); err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, s := range page.Data {
			out[s.ID] = s.ParentID
		}
		return out
	}
	if all := list(""); len(all) != 6 {
		t.Fatalf("unfiltered list = %v; want 3 roots and 3 children", all)
	}
	for key, n := range map[string]int{"p1": 1, "p2": 2, "lone": 0} {
		kids := list("?parentID=" + refs[key].NativeID)
		if len(kids) != n {
			t.Fatalf("children of %s = %v; want %d", key, kids, n)
		}
		for _, p := range kids {
			if p != refs[key].NativeID {
				t.Fatalf("children of %s = %v", key, kids)
			}
		}
	}
	roots := list("?parentID=null")
	if len(roots) != 3 || roots[refs["p1"].NativeID] != "" || roots[refs["p2"].NativeID] != "" || roots[refs["lone"].NativeID] != "" {
		t.Fatalf("roots = %v", roots)
	}
	if _, ok := roots[refs["lone"].NativeID]; !ok {
		t.Fatalf("roots = %v; want the unrelated root", roots)
	}
}

func send(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.SetBasicAuth("opencode", "pw")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// The runtime MCP endpoints Loom's bridge registration uses, per location.
func TestEmulatorMCPRuntimeServers(t *testing.T) {
	_, url, _ := emuURL(t, filepath.Join(t.TempDir(), "state.json"), "")
	at := func(dir string) string { return "?location[directory]=" + neturl.QueryEscape(dir) }
	list := func(dir string) string {
		_, b := send(t, "GET", url+"/api/mcp"+at(dir), "")
		return strings.TrimSpace(b)
	}
	if got := list("/w/a"); got != `{"data":[],"location":{"directory":"/w/a"}}` {
		t.Fatalf("empty list = %s", got)
	}
	put := func(dir, name, cfg string) int {
		code, _ := send(t, "PUT", url+"/api/experimental/mcp/"+name+at(dir), `{"config":`+cfg+`}`)
		return code
	}
	if c := put("/w/a", "loom", `{"type":"local","command":["sh","-c","x"],"environment":{"LOOM_AGENT_TOKEN":"t"}}`); c != 204 {
		t.Fatalf("PUT = %d", c)
	}
	if c := put("/w/a", "off", `{"type":"local","command":["sh"],"disabled":true}`); c != 204 {
		t.Fatalf("PUT disabled = %d", c)
	}
	if c := put("/w/a", "gone", `{"type":"local","command":["/no/such/bridge"]}`); c != 204 {
		t.Fatalf("PUT missing command = %d", c)
	}
	if c := put("/w/a", "bad", `{"type":"local"}`); c != 400 {
		t.Fatalf("PUT without command = %d; want 400", c)
	}
	want := `{"data":[{"name":"gone","status":{"error":"exec: \"/no/such/bridge\": stat /no/such/bridge: no such file or directory","status":"failed"}},` +
		`{"name":"loom","status":{"status":"connected"}},{"name":"off","status":{"status":"disabled"}}],"location":{"directory":"/w/a"}}`
	if got := list("/w/a"); got != want {
		t.Fatalf("list = %s\nwant %s", got, want)
	}
	if got := list("/w/b"); !strings.HasPrefix(got, `{"data":[]`) {
		t.Fatalf("another location lists %s", got)
	}
	if code, _ := send(t, "DELETE", url+"/api/experimental/mcp/loom"+at("/w/a"), ""); code != 204 {
		t.Fatalf("DELETE = %d", code)
	}
	code, b := send(t, "DELETE", url+"/api/experimental/mcp/loom"+at("/w/a"), "")
	if code != 404 || !strings.Contains(b, `"_tag":"McpServerNotFoundError"`) || !strings.Contains(b, "MCP server not found: loom") {
		t.Fatalf("second DELETE = %d %s", code, b)
	}
	if strings.Contains(list("/w/a"), `"loom"`) {
		t.Fatal("removed server still listed")
	}
	if c := put("/w/a", "loom", `{"type":"local","command":["sh"]}`); c != 204 || !strings.Contains(list("/w/a"), `{"name":"loom","status":{"status":"connected"}}`) {
		t.Fatalf("re-add after remove = %d, %s", c, list("/w/a"))
	}
}

// Echo parity (R29: keep echo's behavior and contracts): every new prompt
// is captured once, in order, with its session, agent and text (echo's
// invocation capture and counting, under concurrency); an unscripted turn
// plays the fake model's reply and sends it the session's user texts, a
// model tool call holds the turn until Stop, and a model error fails the
// turn. Streaming, usage, errors and the scripted sequence are in
// TestEmulatorScenarioTurns.
func TestEmulatorEchoParity(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var bodies []string
	replies := []string{
		`{"choices":[{"delta":{"content":"from "}}]}` + "\n\ndata: " + `{"choices":[{"delta":{"content":"model"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"shell"}}]}}]}`,
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		bodies = append(bodies, string(b))
		if len(replies) == 0 {
			w.WriteHeader(500)
			return
		}
		_, _ = io.WriteString(w, "data: "+replies[0]+"\n\ndata: [DONE]\n\n")
		replies = replies[1:]
	}))
	defer model.Close()
	state := filepath.Join(t.TempDir(), "state.json")
	s, err := harnessemu.New(state, "", "pw")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer func() { s.Close(); srv.Close() }()
	c := opencode.NewClient(srv.URL, "pw")
	f, err := c.Feed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("agent-%d", i)
			ref, err := c.Open(ctx, loomharness.OpenSpec{Key: key, Dir: t.TempDir(), Metadata: map[string]string{"agent_id": key}})
			if err == nil {
				in := loomharness.Input{Key: opencode.PromptID(key, "r1"), Text: "prompt " + key}
				if err = c.Session(ref).Prompt(ctx, in); err == nil {
					err = c.Session(ref).Prompt(ctx, in) // a repeat is not a new prompt
				}
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var st struct{ Prompts []harnessemu.Prompt }
	b, _ := os.ReadFile(state)
	if err := json.Unmarshal(b, &st); err != nil || len(st.Prompts) != n {
		t.Fatalf("captured %d prompts, want %d: %v", len(st.Prompts), n, err)
	}
	for _, p := range st.Prompts {
		if p.Session == "" || p.Text != "prompt "+p.Agent || p.ID != opencode.PromptID(p.Agent, "r1") {
			t.Fatalf("captured %+v", p)
		}
	}

	s.Model = model.URL
	ref, err := c.Open(ctx, loomharness.OpenSpec{Key: "m", Dir: t.TempDir(), Metadata: map[string]string{"agent_id": "m"}})
	if err != nil {
		t.Fatal(err)
	}
	sess := c.Session(ref)
	turn := func(r, text string) []loomharness.Event {
		if err := sess.Prompt(ctx, loomharness.Input{Key: opencode.PromptID("m", r), Text: text}); err != nil {
			t.Fatal(err)
		}
		return until(t, f, func(e loomharness.Event) bool { return completed(e) && e.Session.NativeID == ref.NativeID })
	}
	text := ""
	for _, e := range turn("r1", "first") {
		if e.Type == loomharness.EventDelta && e.Session.NativeID == ref.NativeID {
			text += e.Text
		}
	}
	if text != "from model" {
		t.Fatalf("model turn text = %q", text)
	}
	if err := sess.Prompt(ctx, loomharness.Input{Key: opencode.PromptID("m", "r2"), Text: "second"}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if st, _ := sess.Status(ctx); st.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tool-call turn never ran")
		}
	}
	time.Sleep(50 * time.Millisecond)
	if st, _ := sess.Status(ctx); !st.Running {
		t.Fatal("a tool-call turn ended without Stop")
	}
	if ok, err := sess.Interrupt(ctx); err != nil || !ok {
		t.Fatalf("Interrupt = %v, %v", ok, err)
	}
	if e := until(t, f, func(e loomharness.Event) bool { return completed(e) && e.Session.NativeID == ref.NativeID }); e[len(e)-1].StopReason != "cancelled" {
		t.Fatalf("stopped tool-call turn = %+v", e[len(e)-1])
	}
	if e := turn("r3", "third"); e[len(e)-1].StopReason != "failed" {
		t.Fatalf("model error turn = %+v", e[len(e)-1])
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 3 || !strings.Contains(bodies[0], `"content":"first"`) || !strings.Contains(bodies[2], `"content":"second"`) ||
		!strings.Contains(bodies[2], `"content":"third"`) {
		t.Fatalf("model requests = %v", bodies)
	}
}
