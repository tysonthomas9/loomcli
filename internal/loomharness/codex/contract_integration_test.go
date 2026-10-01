package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// fakeModel is a Responses-API model for real codex: each request takes the
// next scripted reply, a text answer, or holds until the request ends.
type fakeModel struct {
	mu    sync.Mutex
	steps []string // reply text; "" holds the response open
}

func (m *fakeModel) script(steps ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps = append(m.steps, steps...)
}

func (m *fakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		_, _ = w.Write([]byte(`{"data":[{"id":"m","object":"model"}]}`))
		return
	}
	m.mu.Lock()
	text := "ok"
	if len(m.steps) > 0 {
		text, m.steps = m.steps[0], m.steps[1:]
	}
	m.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	ev := func(typ string, data map[string]any) {
		data["type"] = typ
		b, _ := json.Marshal(data)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, b)
		w.(http.Flusher).Flush()
	}
	ev("response.created", map[string]any{"response": map[string]any{"id": "r1"}})
	if text == "" {
		<-r.Context().Done() // a turn that runs until it is interrupted
		return
	}
	msg := map[string]any{"type": "message", "role": "assistant", "id": "msg-" + fmt.Sprint(time.Now().UnixNano())}
	ev("response.output_item.added", map[string]any{"item": msg})
	ev("response.output_text.delta", map[string]any{"delta": text, "item_id": msg["id"], "output_index": 0, "content_index": 0})
	msg["content"] = []any{map[string]any{"type": "output_text", "text": text}}
	ev("response.output_item.done", map[string]any{"item": msg})
	ev("response.completed", map[string]any{"response": map[string]any{"id": "r1",
		"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}})
}

// TestContract runs the 4.1b adapter against the installed codex on a
// /tmp-owned CODEX_HOME whose only model is fakeModel; the user's own
// ~/.codex is never read. LOOM_REAL_CODEX=1 enables it.
func TestContract(t *testing.T) {
	if os.Getenv("LOOM_REAL_CODEX") != "1" {
		t.Skip("LOOM_REAL_CODEX=1 runs the real codex contract")
	}
	model := &fakeModel{}
	srv := httptest.NewServer(model)
	defer srv.Close()
	tmp, err := os.MkdirTemp("/tmp", "loom-codex-contract-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) }) // a /tmp-owned scratch directory, removed after the adapters stop
	home, codexHome, repo := filepath.Join(tmp, "home"), filepath.Join(tmp, "codex"), filepath.Join(tmp, "repo")
	for _, d := range []string{home, codexHome, repo} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := fmt.Sprintf("model = \"m\"\nmodel_provider = \"fake\"\napproval_policy = \"never\"\nsandbox_mode = \"read-only\"\n"+
		"[model_providers.fake]\nname = \"fake\"\nbase_url = \"%s/v1\"\nwire_api = \"responses\"\n", srv.URL)
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CODEX_HOME=" + codexHome}
	newA := func() *Adapter {
		a := NewAdapter(Config{Bin: os.Getenv("LOOM_CODEX_BIN"), Env: env})
		t.Cleanup(a.Stop)
		return a
	}
	a := newA()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	feed, err := a.Feed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	// until reads the feed up to an event of type typ for thread id.
	until := func(t *testing.T, id string, typ loomharness.EventType) []loomharness.Event {
		t.Helper()
		var seen []loomharness.Event
		for {
			select {
			case e := <-feed.Events():
				if e.Session.NativeID != id {
					continue
				}
				seen = append(seen, e)
				if e.Type == typ {
					return seen
				}
			case <-time.After(2 * time.Minute):
				t.Fatalf("no %s for %s; saw %+v", typ, id, seen)
			}
		}
	}

	var ref loomharness.NativeRef
	t.Run("Open", func(t *testing.T) {
		if ref, err = a.Open(ctx, spec("contract", repo, "")); err != nil {
			t.Fatal(err)
		}
		if ref.Root != a.Root("") || ref.NativeID == "" {
			t.Fatalf("ref %+v, want a thread under %s", ref, a.Root(""))
		}
		again, err := a.Open(ctx, spec("contract", repo, ""))
		if err != nil || again != ref {
			t.Fatalf("repeat Open: %+v %v, want %+v", again, err, ref)
		}
		if l, err := a.Session(ref).HasInput(ctx, "key-1"); err != nil || l != loomharness.LandedNotFound {
			t.Fatalf("HasInput before any prompt: %v %v", l, err)
		}
	})

	var live []loomharness.Event
	t.Run("Prompt", func(t *testing.T) {
		model.script("hello from codex")
		s := a.Session(ref)
		if err := s.Prompt(ctx, loomharness.Input{Key: "key-1", Text: "say hello"}); err != nil {
			t.Fatal(err)
		}
		live = until(t, ref.NativeID, loomharness.EventTurnCompleted)
		var delivered, answered bool
		for _, e := range live {
			delivered = delivered || e.Type == loomharness.EventMessageDelivered && e.InputKey == "key-1" && e.Text == "say hello"
			answered = answered || e.Type == loomharness.EventItemCompleted && e.ItemKind == "message" && e.Text == "hello from codex"
		}
		if last := live[len(live)-1]; !delivered || !answered || last.StopReason != "completed" || last.Session.Root != ref.Root {
			t.Fatalf("live events %+v", live)
		}
		if l, err := s.HasInput(ctx, "key-1"); err != nil || l != loomharness.LandedFound {
			t.Fatalf("HasInput: %v %v", l, err)
		}
	})

	t.Run("Messages", func(t *testing.T) {
		page, err := a.Session(ref).Messages(ctx, "", 50)
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, e := range page.Events {
			ids[e.ItemID] = true
		}
		for _, e := range live {
			if (e.Type == loomharness.EventMessageDelivered || e.Type == loomharness.EventItemCompleted) && !ids[e.ItemID] {
				t.Fatalf("live item %s (%s) missing from Messages %+v", e.ItemID, e.Type, page.Events)
			}
		}
	})

	t.Run("Interrupt", func(t *testing.T) {
		model.script("") // holds the turn open
		s := a.Session(ref)
		if err := s.Prompt(ctx, loomharness.Input{Key: "key-2", Text: "run long"}); err != nil {
			t.Fatal(err)
		}
		until(t, ref.NativeID, loomharness.EventTurnStarted)
		deadline := time.Now().Add(time.Minute)
		for st, _ := s.Status(ctx); st.TurnID == "" && time.Now().Before(deadline); st, _ = s.Status(ctx) {
			time.Sleep(50 * time.Millisecond)
		}
		if err := s.Prompt(ctx, loomharness.Input{Key: "key-3", Text: "steer"}); !errors.Is(err, loomharness.ErrBusy) {
			t.Fatalf("Prompt while running: %v, want ErrBusy", err)
		}
		if ok, err := s.Interrupt(ctx); !ok || err != nil {
			t.Fatalf("Interrupt: %v %v", ok, err)
		}
		done := until(t, ref.NativeID, loomharness.EventTurnCompleted)
		if done[len(done)-1].StopReason != "cancelled" {
			t.Fatalf("interrupted turn: %+v", done[len(done)-1])
		}
		st, err := s.Status(ctx)
		if err != nil || st.Running || st.TurnID != "" || !st.LastTurnInterrupt {
			t.Fatalf("status after interrupt: %+v %v", st, err)
		}
		if ok, err := s.Interrupt(ctx); ok || err != nil {
			t.Fatalf("Interrupt when idle: %v %v", ok, err)
		}
	})

	t.Run("Orphan", func(t *testing.T) {
		a.Stop() // a crash lost the record; a new Loom process opens the key again
		b := newA()
		adopted, err := b.Open(ctx, spec("contract", repo, ""))
		if err != nil || adopted != ref {
			t.Fatalf("after restart: %+v %v, want %+v", adopted, err, ref)
		}
		if err := b.Purge(ctx, []loomharness.NativeRef{ref}); err != nil {
			t.Fatal(err)
		}
		if err := b.Purge(ctx, []loomharness.NativeRef{ref}); err != nil {
			t.Fatalf("second purge: %v", err)
		}
		if _, err := b.Session(ref).Status(ctx); !errors.Is(err, loomharness.ErrSessionNotFound) {
			t.Fatalf("status after purge: %v, want ErrSessionNotFound", err)
		}
		fresh, err := b.Open(ctx, spec("contract", repo, ""))
		if err != nil || fresh.NativeID == ref.NativeID {
			t.Fatalf("Open after purge: %+v %v, want a new thread", fresh, err)
		}
	})
}
