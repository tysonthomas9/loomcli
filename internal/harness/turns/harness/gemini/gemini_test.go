package gemini

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/harness/screen"
	"github.com/tysonthomas9/loomcli/internal/harness/turns"
	"github.com/tysonthomas9/loomcli/internal/harness/wrapper"
)

func TestGeminiAdapterName(t *testing.T) {
	if n := New().Name(); n != "gemini" {
		t.Errorf("expected Name()=gemini, got %q", n)
	}
}

// Until a corpus-derived screen marker lands, OnScreen should produce
// no events — the generic.Adapter delegate does nothing on screen
// changes by design. This locks in that behavior so a future override
// is an intentional change, not a regression.
func TestGeminiAdapterNoScreenEventsByDefault(t *testing.T) {
	scr := screen.New(120, 40)
	scr.Write([]byte("any old content\r\n"))
	if evs := New().OnScreen(scr.Snapshot()); len(evs) != 0 {
		t.Errorf("expected 0 OnScreen events from generic delegate, got %d: %+v", len(evs), evs)
	}
}

// Verify the generic delegate still fires turn-complete via the
// wrapper.StatusWaitingForInput path. This is the fallback the adapter
// relies on for chat-style flows until a per-harness marker exists.
func TestGeminiAdapterFiresOnWaitingForInput(t *testing.T) {
	evs := New().OnWrapperStatus(wrapper.StatusWaitingForInput, "prompt detected: (y/n)")
	if len(evs) != 1 || evs[0].Kind != turns.TurnComplete {
		t.Errorf("expected 1 TurnComplete event, got %+v", evs)
	}
}

func TestGeminiAdapterNoSessionIDYet(t *testing.T) {
	scr := screen.New(120, 40)
	scr.Write([]byte("gemini --resume 0281fd4a-0a10-4dfe-adca-9b61b3777255\r\n"))
	if id, ok := New().ExtractSessionID(scr.Snapshot()); ok {
		t.Errorf("expected no session id yet, got %q", id)
	}
}
