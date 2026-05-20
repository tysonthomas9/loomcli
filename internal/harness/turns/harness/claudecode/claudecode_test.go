package claudecode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/harness/screen"
	"github.com/tysonthomas9/loomcli/internal/harness/turns"
)

func corpusBytes(t *testing.T, scenario string) []byte {
	t.Helper()
	wd, _ := os.Getwd()
	for i := 0; i < 6; i++ {
		p := filepath.Join(wd, "test/corpus/claude-code", scenario, "bytes.raw")
		if data, err := os.ReadFile(p); err == nil {
			return data
		}
		wd = filepath.Dir(wd)
	}
	t.Skipf("test/corpus/claude-code/%s/bytes.raw not found", scenario)
	return nil
}

func TestClaudeCodeAdapterFiresOnMultiTurn(t *testing.T) {
	bytes := corpusBytes(t, "multi-turn")

	scr := screen.New(120, 40)
	scr.Write(bytes)
	snap := scr.Snapshot()

	a := New()
	evs := a.OnScreen(snap)
	if len(evs) != 1 {
		t.Fatalf("expected 1 event from final snapshot, got %d: %+v", len(evs), evs)
	}
	if evs[0].Kind != turns.TurnComplete {
		t.Errorf("expected TurnComplete, got %s", evs[0].Kind)
	}
}

func TestClaudeCodeAdapterDetectsInterrupt(t *testing.T) {
	bytes := corpusBytes(t, "interrupted-mid-reply")

	scr := screen.New(120, 40)
	scr.Write(bytes)
	snap := scr.Snapshot()

	a := New()
	evs := a.OnScreen(snap)

	var sawErrored bool
	for _, ev := range evs {
		if ev.Kind == turns.Errored {
			sawErrored = true
		}
	}
	if !sawErrored {
		t.Errorf("expected Errored event for interrupted recording, got events: %+v", evs)
	}
}

func TestClaudeCodeAdapterRefiresAcrossTurns(t *testing.T) {
	scr := screen.New(120, 40)
	a := New()

	scr.Write([]byte("⏺ first reply\r\n✻ Baked for 5s\r\n"))
	if evs := a.OnScreen(scr.Snapshot()); len(evs) != 1 {
		t.Fatalf("turn 1: expected 1 event, got %d", len(evs))
	}

	// Same fingerprint → no fire.
	if evs := a.OnScreen(scr.Snapshot()); len(evs) != 0 {
		t.Fatalf("repeat: expected 0 events, got %d", len(evs))
	}

	// New thinking summary → fire again.
	scr.Write([]byte("⏺ second reply\r\n✻ Brewed for 8s\r\n"))
	if evs := a.OnScreen(scr.Snapshot()); len(evs) != 1 {
		t.Fatalf("turn 2: expected 1 event, got %d", len(evs))
	}
}

func TestClaudeCodeAdapterName(t *testing.T) {
	if n := New().Name(); n != "claude-code" {
		t.Errorf("expected Name()=claude-code, got %q", n)
	}
}

// TestClaudeCodeAdapter_AdversarialNoFire feeds the adapter a recording
// where the assistant echoes the "✻ <Verb> for Ns" marker shape inside
// explanatory prose without actually completing the turn. Before the
// thinkingRE was anchored to a line of its own, this scenario
// mis-fired TurnComplete; the test locks the anchored behavior in.
func TestClaudeCodeAdapter_AdversarialNoFire(t *testing.T) {
	bytes := corpusBytes(t, "adversarial/thinking-line-mid-reply")

	scr := screen.New(120, 40)
	scr.Write(bytes)
	snap := scr.Snapshot()

	a := New()
	evs := a.OnScreen(snap)
	for _, ev := range evs {
		if ev.Kind == turns.TurnComplete {
			t.Errorf("adversarial thinking-line-mid-reply mis-fired TurnComplete: %+v", ev)
		}
	}
}
