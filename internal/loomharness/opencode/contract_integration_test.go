package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// TestContract runs the adapter against the pinned b30c4d0 build in a /tmp
// sandbox (own HOME and XDG roots) with a local fake OpenAI-compatible model,
// so no login, network or user data is touched. It purges every session it
// opens. LOOM_REAL_OPENCODE=1 enables it; LOOM_OPENCODE_BIN overrides the
// binary.
func TestContract(t *testing.T) {
	if os.Getenv("LOOM_REAL_OPENCODE") != "1" {
		t.Skip("set LOOM_REAL_OPENCODE=1 to run against the real OpenCode build")
	}
	bin := os.Getenv("LOOM_OPENCODE_BIN")
	if bin == "" {
		home, _ := os.UserHomeDir()
		bin = filepath.Join(home, ".loom/harness/opencode/2.0.19/opencode")
	}
	model := newFakeModel(t)
	sbx, err := os.MkdirTemp("/tmp", "loom-opencode-contract-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sbx) })
	repo, moved := filepath.Join(sbx, "repo"), filepath.Join(sbx, "moved")
	for _, d := range []string{filepath.Join(sbx, "home"), filepath.Join(sbx, "tmp"), filepath.Join(sbx, "config/opencode"), repo, moved} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	userConfig := fmt.Sprintf(`{"provider":{"fake":{"name":"Fake","npm":"@ai-sdk/openai-compatible",
		"options":{"baseURL":%q,"apiKey":"x"},
		"models":{"m":{"name":"M","limit":{"context":100000,"output":4000}},"m2":{"name":"M2","limit":{"context":100000,"output":4000}}}}},
		"model":"fake/m"}`, model.URL+"/v1")
	if err := os.WriteFile(filepath.Join(sbx, "config/opencode/opencode.json"), []byte(userConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	a := New(Config{Bin: bin, Env: contractEnv(sbx), Presets: []loomharness.PresetConfig{{Name: "tester", Persona: "LOOM-PERSONA-MARKER"}}})
	var owned []loomharness.NativeRef
	t.Cleanup(func() {
		if err := a.Purge(context.Background(), owned); err != nil {
			t.Errorf("cleanup purge: %v", err)
		}
		pid := serverPID(a)
		a.Stop()
		if alive(pid) {
			t.Errorf("server %d still running after Stop", pid)
		}
	})

	h, err := a.Health(ctx)
	if err != nil || !h.OK || h.Version.Installed.String() != "2.0.19" {
		t.Fatalf("Health = %+v, %v", h, err)
	}
	// The model snapshot fills in after OpenCode's plugins settle.
	waitFor(t, "fake/m in Models", func() bool { models, err := a.Models(ctx); return err == nil && hasModel(models, "fake/m") })
	feed, err := a.Feed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer feed.Close()
	events := collect(feed)

	spec := loomharness.OpenSpec{
		Key: "agent-1", Launch: loomharness.Launch{Root: sbx}, Preset: loomharness.PresetConfig{Name: "tester"},
		Dir: repo, Model: "fake/m", Metadata: map[string]string{"loom_agent_id": "agent-1"},
		Rules: []loomharness.PermissionRule{{Action: "bash", Resource: "*", Effect: "ask"}},
	}
	ref, err := a.Open(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	owned = append(owned, ref)
	if again, err := a.Open(ctx, spec); err != nil || again != ref {
		t.Fatalf("repeat Open = %v, %v; want %v", again, err, ref)
	}
	s := a.Session(ref)
	sp := func(suffix string) string { return "/api/session/" + ref.NativeID + suffix }

	t.Run("PromptRetry", func(t *testing.T) {
		key := PromptID("agent-1", "req-1")
		for range 2 {
			if err := s.Prompt(ctx, loomharness.Input{Key: key, Text: "hello"}); err != nil {
				t.Fatal(err)
			}
		}
		events.wait(t, "turn completed", func(e loomharness.Event) bool {
			return e.Session.NativeID == ref.NativeID && e.Type == loomharness.EventTurnCompleted && e.StopReason == "completed"
		})
		if got, err := s.HasInput(ctx, key); err != nil || got != loomharness.LandedFound {
			t.Fatalf("HasInput = %v, %v", got, err)
		}
		if got, err := s.HasInput(ctx, PromptID("agent-1", "never")); err != nil || got != loomharness.LandedNotFound {
			t.Fatalf("HasInput(absent) = %v, %v", got, err)
		}
		delivered := 0
		for _, e := range allEvents(t, s, 100) {
			if e.Type == loomharness.EventMessageDelivered && e.InputKey == key {
				delivered++
			}
		}
		if delivered != 1 || model.requests("hello") != 1 {
			t.Fatalf("retry: %d delivered messages, %d model turns; want 1 and 1", delivered, model.requests("hello"))
		}
		if !model.sawSystem("LOOM-PERSONA-MARKER") {
			t.Fatal("the loom-tester preset persona never reached the model (preset merge)")
		}
	})

	t.Run("Paging", func(t *testing.T) {
		whole := allEvents(t, s, 100)
		paged := allEvents(t, s, 1)
		if len(whole) < 3 || len(paged) != len(whole) {
			t.Fatalf("paged %d events, whole %d", len(paged), len(whole))
		}
		for i := range whole {
			if paged[i].ItemID != whole[i].ItemID || paged[i].Type != whole[i].Type {
				t.Fatalf("event %d: paged %+v, whole %+v", i, paged[i], whole[i])
			}
		}
	})

	t.Run("Interrupt", func(t *testing.T) {
		if err := s.Prompt(ctx, loomharness.Input{Key: PromptID("agent-1", "req-2"), Text: "SLOW please"}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "running turn", func() bool { st, err := s.Status(ctx); return err == nil && st.Running && model.requests("SLOW") > 0 })
		if ok, err := s.Interrupt(ctx); err != nil || !ok {
			t.Fatalf("Interrupt = %v, %v", ok, err)
		}
		events.wait(t, "cancelled turn", func(e loomharness.Event) bool {
			return e.Session.NativeID == ref.NativeID && e.Type == loomharness.EventTurnCompleted && e.StopReason == "cancelled"
		})
		if st, err := s.Status(ctx); err != nil || st.Running {
			t.Fatalf("Status after interrupt = %+v, %v", st, err)
		}
		if ok, err := s.Interrupt(ctx); err != nil || ok {
			t.Fatalf("idle Interrupt = %v, %v; want false", ok, err)
		}
	})

	t.Run("PermissionReply", func(t *testing.T) {
		done := make(chan error, 1)
		go func() {
			done <- a.call(ctx, "POST", sp("/permission"), map[string]any{"action": "bash", "resources": []string{"echo hi"}}, nil)
		}()
		var askID string
		waitFor(t, "pending permission", func() bool {
			var l struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if a.call(ctx, "GET", sp("/permission"), nil, &l) == nil && len(l.Data) == 1 {
				askID = l.Data[0].ID
			}
			return askID != ""
		})
		if err := s.Reply(ctx, askID, loomharness.Reply{Allow: true}); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatalf("permission ask: %v", err)
		}
		if err := s.Reply(ctx, askID, loomharness.Reply{Allow: true}); !isCode(err, "ask_missing") {
			t.Fatalf("second Reply = %v; want ask_missing", err)
		}
	})

	t.Run("QuestionReply", func(t *testing.T) {
		var form struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		body := map[string]any{"title": "Which color?", "fields": []map[string]string{{"key": "color", "type": "string"}}}
		if err := a.call(ctx, "POST", sp("/form"), body, &form); err != nil {
			t.Fatal(err)
		}
		if err := s.Reply(ctx, form.Data.ID, loomharness.Reply{Answer: "blue"}); err != nil {
			t.Fatal(err)
		}
		var got struct {
			Data struct {
				State struct {
					Status string `json:"status"`
				} `json:"state"`
			} `json:"data"`
		}
		if err := a.call(ctx, "GET", sp("/form/"+form.Data.ID), nil, &got); err != nil || got.Data.State.Status == "pending" {
			t.Fatalf("form after Reply = %+v, %v", got, err)
		}
		if err := s.Reply(ctx, form.Data.ID, loomharness.Reply{Answer: "red"}); !isCode(err, "ask_missing") {
			t.Fatalf("second Reply = %v; want ask_missing", err)
		}
		if err := s.Reply(ctx, "frm_missing", loomharness.Reply{Answer: "red"}); !isCode(err, "ask_missing") {
			t.Fatalf("Reply(missing form) = %v; want ask_missing", err)
		}
	})

	t.Run("SetModelAndMove", func(t *testing.T) {
		if err := s.SetModel(ctx, "fake/m2"); err != nil {
			t.Fatal(err)
		}
		if err := s.Move(ctx, moved); err != nil {
			t.Fatal(err)
		}
		info := sessionInfo(t, a, ref)
		if info.Model.ID != "m2" || info.Location.Directory != moved {
			t.Fatalf("session = %+v; want model m2 in %s", info, moved)
		}
	})

	t.Run("RestartResume", func(t *testing.T) {
		pid := serverPID(a)
		if err := a.Restart(ctx); err != nil {
			t.Fatal(err)
		}
		// Service mode generates the password once and keeps it in the
		// service config; the adapter reads it from service.json each boot.
		if _, pw := a.endpoint(); serverPID(a) == pid || alive(pid) || len(pw) < 32 {
			t.Fatalf("restart: pid %d -> %d (old alive %v), password length %d", pid, serverPID(a), alive(pid), len(pw))
		}
		got, err := s.Resume(ctx, spec.Launch)
		if err != nil || got != ref {
			t.Fatalf("Resume = %v, %v; want %v", got, err, ref)
		}
		if landed, err := s.HasInput(ctx, PromptID("agent-1", "req-1")); err != nil || landed != loomharness.LandedFound {
			t.Fatalf("HasInput after restart = %v, %v", landed, err)
		}
		events.wait(t, "feed gap after restart", func(e loomharness.Event) bool { return e.Type == loomharness.EventFeedGap })
		if err := s.Prompt(ctx, loomharness.Input{Key: PromptID("agent-1", "req-3"), Text: "after restart"}); err != nil {
			t.Fatal(err)
		}
		events.wait(t, "turn after restart", func(e loomharness.Event) bool {
			return e.Session.NativeID == ref.NativeID && e.Type == loomharness.EventTurnCompleted && e.StopReason == "completed" && model.requests("after restart") == 1
		})
	})

	t.Run("PurgeOwnedOnly", func(t *testing.T) {
		sibling, err := a.Open(ctx, loomharness.OpenSpec{Key: "sibling", Launch: spec.Launch, Dir: repo})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = a.Purge(context.Background(), []loomharness.NativeRef{sibling}) }()
		if err := a.Purge(ctx, owned); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Resume(ctx, spec.Launch); !isCode(err, "session_missing") {
			t.Fatalf("Resume after Purge = %v; want session_missing", err)
		}
		if _, err := a.Session(sibling).Resume(ctx, spec.Launch); err != nil {
			t.Fatalf("sibling after Purge: %v", err)
		}
		if err := a.Purge(ctx, owned); err != nil {
			t.Fatalf("repeat Purge: %v", err)
		}
	})
}

// contractEnv is the sandbox environment: OpenCode's HOME, TMPDIR and XDG
// roots under sbx, no provider keys, and no models.dev fetch.
func contractEnv(sbx string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "OPENCODE_") || strings.HasPrefix(k, "XDG_") || strings.HasSuffix(k, "_API_KEY") || k == "HOME" || k == "TMPDIR" {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"HOME="+filepath.Join(sbx, "home"),
		"TMPDIR="+filepath.Join(sbx, "tmp")+"/",
		"XDG_DATA_HOME="+filepath.Join(sbx, "data"),
		"XDG_CONFIG_HOME="+filepath.Join(sbx, "config"),
		"XDG_STATE_HOME="+filepath.Join(sbx, "state"),
		"XDG_CACHE_HOME="+filepath.Join(sbx, "cache"),
		"OPENCODE_DISABLE_MODELS_FETCH=1",
	)
}

func hasModel(models []loomharness.Model, id string) bool {
	for _, m := range models {
		if m.ID == id {
			return true
		}
	}
	return false
}

func allEvents(t *testing.T, s loomharness.Session, limit int) []loomharness.Event {
	t.Helper()
	var out []loomharness.Event
	for after := ""; ; {
		page, err := s.Messages(context.Background(), after, limit)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, page.Events...)
		if page.Next == "" {
			return out
		}
		after = page.Next
	}
}

type sessionView struct {
	Model struct {
		ID string `json:"id"`
	} `json:"model"`
	Location struct {
		Directory string `json:"directory"`
	} `json:"location"`
}

func sessionInfo(t *testing.T, a *Adapter, ref loomharness.NativeRef) sessionView {
	t.Helper()
	var r struct {
		Data sessionView `json:"data"`
	}
	if err := a.call(context.Background(), "GET", "/api/session/"+ref.NativeID, nil, &r); err != nil {
		t.Fatal(err)
	}
	return r.Data
}

// eventLog keeps every feed event for later waits.
type eventLog struct {
	mu     sync.Mutex
	events []loomharness.Event
}

func collect(f loomharness.Feed) *eventLog {
	l := &eventLog{}
	go func() {
		for e := range f.Events() {
			l.mu.Lock()
			l.events = append(l.events, e)
			l.mu.Unlock()
		}
	}()
	return l
}

func (l *eventLog) wait(t *testing.T, what string, match func(loomharness.Event) bool) {
	t.Helper()
	waitFor(t, what, func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		for _, e := range l.events {
			if match(e) {
				return true
			}
		}
		return false
	})
}

// fakeModel is an OpenAI-compatible chat completions server. A turn whose
// last message contains SLOW streams one chunk and then holds until the
// request is cancelled.
type fakeModel struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []string
}

func newFakeModel(t *testing.T) *fakeModel {
	m := &fakeModel{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(raw, &req)
		last := ""
		if n := len(req.Messages); n > 0 {
			last = string(req.Messages[n-1].Content)
		}
		m.mu.Lock()
		m.bodies = append(m.bodies, string(raw))
		m.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := func(delta map[string]string, finish any) {
			b, _ := json.Marshal(map[string]any{"id": "c1", "object": "chat.completion.chunk", "created": 1, "model": "m",
				"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
			w.(http.Flusher).Flush()
		}
		chunk(map[string]string{"role": "assistant", "content": "working "}, nil)
		if strings.Contains(last, "SLOW") {
			select {
			case <-r.Context().Done():
			case <-time.After(time.Minute):
			}
			return
		}
		chunk(map[string]string{"content": "done"}, nil)
		chunk(map[string]string{}, "stop")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(m.Close)
	return m
}

// requests counts agent turns (not title requests) whose last message
// contains text.
func (m *fakeModel) requests(text string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, b := range m.bodies {
		var req struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if strings.Contains(b, "You are a title generator") {
			continue
		}
		if json.Unmarshal([]byte(b), &req) == nil && len(req.Messages) > 0 && strings.Contains(string(req.Messages[len(req.Messages)-1].Content), text) {
			n++
		}
	}
	return n
}

func (m *fakeModel) sawSystem(text string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.bodies {
		if strings.Contains(b, text) {
			return true
		}
	}
	return false
}
