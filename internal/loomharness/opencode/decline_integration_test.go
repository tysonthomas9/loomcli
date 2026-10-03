package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// TestDeclineEndsTheTurn (OC1) runs a shell tool call that asks on the
// pinned build, with a fake model, and rejects it. OpenCode ends that
// execution as a "shutdown" interrupt with no idle marker and runs nothing
// more; the feed must still end the turn, as declined. LOOM_REAL_OPENCODE=1
// enables it.
func TestDeclineEndsTheTurn(t *testing.T) {
	bin := realOpenCode(t)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(v any) {
			b, _ := json.Marshal(v)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
			w.(http.Flusher).Flush()
		}
		chunk := func(delta map[string]any, finish any) {
			send(map[string]any{"id": "c1", "object": "chat.completion.chunk", "created": 1, "model": "m",
				"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}}})
		}
		if strings.Contains(string(raw), "TOOLCALL") && !strings.Contains(string(raw), `"role":"tool"`) && !strings.Contains(string(raw), "title generator") {
			chunk(map[string]any{"role": "assistant", "tool_calls": []map[string]any{{"index": 0, "id": "call_1", "type": "function",
				"function": map[string]any{"name": "shell", "arguments": `{"command":"echo hi","description":"say hi"}`}}}}, nil)
			chunk(map[string]any{}, "tool_calls")
		} else {
			chunk(map[string]any{"role": "assistant", "content": "done"}, nil)
			chunk(map[string]any{}, "stop")
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(model.Close)
	sbx := newSandbox(t, "loom-opencode-decline-", fakeModelConfig(model.URL))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	a := New(Config{Bin: bin, Env: contractEnv(sbx)})
	t.Cleanup(a.Stop)
	waitFor(t, "fake/m", func() bool { m, err := a.Models(ctx); return err == nil && hasModel(m, "fake/m") })
	feed, err := a.Feed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer feed.Close()
	events := collect(feed)
	ref, err := a.Open(ctx, loomharness.OpenSpec{Key: "agent-1", Launch: loomharness.Launch{Root: sbx}, Dir: sbx + "/repo", Model: "fake/m",
		Rules: []loomharness.PermissionRule{{Action: "*", Resource: "*", Effect: "allow"}, {Action: "bash", Resource: "*", Effect: "ask"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Purge(context.Background(), []loomharness.NativeRef{ref}) })
	s := a.Session(ref)
	if err := s.Prompt(ctx, loomharness.Input{Key: PromptID("agent-1", "r1"), Text: "TOOLCALL please"}); err != nil {
		t.Fatal(err)
	}
	var askID string
	events.wait(t, "ask", func(e loomharness.Event) bool {
		if e.Type == loomharness.EventAskOpened {
			askID = e.AskID
			return true
		}
		return false
	})
	if err := s.Reply(ctx, askID, loomharness.Reply{}); err != nil {
		t.Fatal(err)
	}
	events.wait(t, "the declined turn's end", func(e loomharness.Event) bool {
		return e.Session.NativeID == ref.NativeID && e.Type == loomharness.EventTurnCompleted && e.StopReason == "declined"
	})
	if st, err := s.Status(ctx); err != nil || st.Running {
		t.Fatalf("Status after the reject = %+v, %v; want nothing running", st, err)
	}
	if ok, err := s.Interrupt(ctx); err != nil || ok {
		t.Fatalf("Interrupt after the reject = %v, %v; want false, nothing runs", ok, err)
	}
	// The next input is its own turn: OpenCode wrote no idle marker, so
	// history must end the declined turn at its declined step.
	key := PromptID("agent-1", "r2")
	if err := s.Prompt(ctx, loomharness.Input{Key: key, Text: "carry on"}); err != nil {
		t.Fatal(err)
	}
	var next string
	events.wait(t, "the next turn's start with its own input", func(e loomharness.Event) bool {
		if e.Session.NativeID == ref.NativeID && e.Type == loomharness.EventTurnStarted && e.InputKey == key {
			next = e.TurnID
			return true
		}
		return false
	})
	events.wait(t, "the next turn's end", func(e loomharness.Event) bool {
		return e.TurnID == next && e.Type == loomharness.EventTurnCompleted && e.StopReason == "completed"
	})
}
