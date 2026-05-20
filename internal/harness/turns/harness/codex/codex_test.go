package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/harness/screen"
	"github.com/tysonthomas9/loomcli/internal/harness/turns"
)

// corpusBytes loads a recorded byte stream from the project's test/corpus.
// Returns the bytes or t.Skip-ed test if the recording isn't present.
func corpusBytes(t *testing.T, scenario string) []byte {
	t.Helper()
	// Walk up from this test's CWD (pkg/turns/harness/codex) to find repo root.
	wd, _ := os.Getwd()
	for i := 0; i < 6; i++ {
		p := filepath.Join(wd, "test/corpus/codex", scenario, "bytes.raw")
		if data, err := os.ReadFile(p); err == nil {
			return data
		}
		wd = filepath.Dir(wd)
	}
	t.Skipf("test/corpus/codex/%s/bytes.raw not found; record it with screenbench-record", scenario)
	return nil
}

func TestCodexAdapterFiresOnRealRecording(t *testing.T) {
	bytes := corpusBytes(t, "short-reply")

	scr := screen.New(120, 40)
	scr.Write(bytes)
	snap := scr.Snapshot()

	a := New()

	// First call: marker present, fires TurnComplete exactly once.
	evs := a.OnScreen(snap)
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(evs), evs)
	}
	if evs[0].Kind != turns.TurnComplete {
		t.Errorf("expected Kind=TurnComplete, got %s", evs[0].Kind)
	}

	// Second call with same snapshot: marker unchanged → no event.
	if evs := a.OnScreen(snap); len(evs) != 0 {
		t.Errorf("expected 0 events on second call, got %d", len(evs))
	}
}

func TestCodexAdapterNoFireOnEmptyScreen(t *testing.T) {
	scr := screen.New(80, 24)
	a := New()
	if evs := a.OnScreen(scr.Snapshot()); len(evs) != 0 {
		t.Errorf("expected no events on empty screen, got %d", len(evs))
	}
}

func TestCodexAdapterRefiresWhenFingerprintChanges(t *testing.T) {
	scr := screen.New(120, 40)
	a := New()

	scr.Write([]byte("\x1b[H\x1b[2JToken usage: total=100 input=80 (+ 50 cached) output=20\r\n"))
	if evs := a.OnScreen(scr.Snapshot()); len(evs) != 1 {
		t.Fatalf("first turn: expected 1 event, got %d", len(evs))
	}

	// Same fingerprint → no fire.
	if evs := a.OnScreen(scr.Snapshot()); len(evs) != 0 {
		t.Fatalf("repeat: expected 0 events, got %d", len(evs))
	}

	// New fingerprint → fire again.
	scr.Write([]byte("\r\nToken usage: total=200 input=150 (+ 100 cached) output=50\r\n"))
	if evs := a.OnScreen(scr.Snapshot()); len(evs) != 1 {
		t.Fatalf("second turn: expected 1 event, got %d", len(evs))
	}
}

func TestCodexAdapterName(t *testing.T) {
	if n := New().Name(); n != "codex" {
		t.Errorf("expected Name()=codex, got %q", n)
	}
}

// TestCodexAdapter_AdversarialNoFire feeds the adapter recordings that
// should NOT fire TurnComplete: an assistant reply that mentions
// "Token usage:" without the full footer pattern, and a truncated
// stream with no footer at all. Locks in that the footer regex stays
// strict — loosening it (e.g. to plain `"Token usage:"`) would break
// these cases.
func TestCodexAdapter_AdversarialNoFire(t *testing.T) {
	for _, scenario := range []string{
		"adversarial/prefix-only-marker",
		"adversarial/partial-stream-no-footer",
	} {
		t.Run(scenario, func(t *testing.T) {
			bytes := corpusBytes(t, scenario)

			scr := screen.New(120, 40)
			scr.Write(bytes)
			snap := scr.Snapshot()

			a := New()
			evs := a.OnScreen(snap)
			for _, ev := range evs {
				if ev.Kind == turns.TurnComplete {
					t.Errorf("adversarial scenario %q mis-fired TurnComplete: %+v", scenario, ev)
				}
			}
		})
	}
}
