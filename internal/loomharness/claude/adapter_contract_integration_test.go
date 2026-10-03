package claude

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// TestAdapterContract checks the adapter's event mapping and turn-boundary
// switches on the real claude.
func TestAdapterContract(t *testing.T) {
	bin := realBin(t)
	a := New(Config{Bin: bin, Env: hostEnv(), Args: noTools})
	feed, err := a.Feed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	dir, err := os.MkdirTemp("", "loom-claude-adapter-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	l := ownedLaunch(t)
	ctx := context.Background()
	if err := isolated(a.cfg, ProcessSpec{Launch: l, Dir: dir}); err != nil {
		t.Fatal(err)
	}
	authAccepted(t, NewProcess(a.cfg, ProcessSpec{Launch: l, Dir: dir}))
	ref, err := a.Open(ctx, loomharness.OpenSpec{Key: uuid.NewString(), Launch: l, Dir: dir, Model: "haiku"})
	if err != nil {
		t.Fatal(err)
	}
	s := a.Session(ref).(*Session)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	turn := func(t *testing.T, text string) (string, []loomharness.Event) {
		t.Helper()
		key := uuid.NewString()
		if err := s.Prompt(ctx, loomharness.Input{Key: key, Text: text}); err != nil {
			t.Fatal(err)
		}
		var got []loomharness.Event
		for timeout := time.After(5 * time.Minute); ; {
			select {
			case e := <-feed.Events():
				got = append(got, e)
				if e.Type == loomharness.EventTurnCompleted {
					return key, got
				}
			case <-timeout:
				t.Fatal("no turn.completed")
			}
		}
	}
	text := func(es []loomharness.Event) string {
		var b strings.Builder
		for _, e := range es {
			if e.Type == loomharness.EventItemCompleted && e.ItemKind == "message" {
				b.WriteString(e.Text)
			}
		}
		return b.String()
	}
	args := func() []string {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.proc.mu.Lock()
		defer s.proc.mu.Unlock()
		return s.proc.cmd.Args
	}

	t.Run("Frames", func(t *testing.T) {
		_, got := turn(t, "Reply with exactly the word PONG and nothing else.")
		if got[0].Type != loomharness.EventTurnStarted || got[len(got)-1].StopReason != "completed" || !strings.Contains(text(got), "PONG") ||
			!slices.ContainsFunc(got, func(e loomharness.Event) bool { return e.Type == loomharness.EventDelta }) {
			t.Fatalf("events = %+v", got)
		}
		for _, e := range got {
			if e.TurnID != got[0].TurnID || e.Session != ref {
				t.Fatalf("event %+v outside turn %s", e, got[0].TurnID)
			}
		}
	})
	t.Run("Delivery", func(t *testing.T) {
		key, got := turn(t, "Reply with exactly the word OK.")
		if !slices.ContainsFunc(got, func(e loomharness.Event) bool {
			return e.Type == loomharness.EventMessageDelivered && e.InputKey == key
		}) {
			t.Fatalf("no message.delivered for %s in %v", key, types(got))
		}
	})
	// The moved dir lives for the whole test: Model and Close still run in it.
	moved, err := os.MkdirTemp("", "loom-claude-moved-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(moved) })
	t.Run("Move", func(t *testing.T) {
		// Move runs before Model, on the haiku process, so only the dir
		// changes; Model's turn is then the run's only sonnet turn.
		if err := s.Move(ctx, moved); err != nil {
			t.Fatal(err)
		}
		_, got := turn(t, "Reply with only the absolute path of your current working directory.")
		if a := args(); !slices.Contains(a, "--resume") || !slices.Contains(a, ref.NativeID) || !strings.Contains(text(got), filepath.Base(moved)) {
			t.Fatalf("move: args %v, text %q", a, text(got))
		}
	})
	t.Run("Model", func(t *testing.T) {
		if err := s.SetModel(ctx, "sonnet", nil); err != nil {
			t.Fatal(err)
		}
		_, got := turn(t, "Reply with exactly the word MODEL.")
		if a := args(); !slices.Contains(a, "sonnet") || !slices.Contains(a, "--resume") || !strings.Contains(text(got), "MODEL") {
			t.Fatalf("model switch: args %v, text %q", a, text(got))
		}
	})
	t.Run("Close", func(t *testing.T) {
		if err := s.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if st, _ := s.Status(ctx); st.Running {
			t.Fatal("still running after Close")
		}
		if m, _ := filepath.Glob(filepath.Join(l.Root, "projects", "*", ref.NativeID+".jsonl")); len(m) != 1 {
			t.Fatalf("Close must keep the transcript; found %v", m)
		}
	})
}
