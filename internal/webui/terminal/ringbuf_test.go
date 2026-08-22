package terminal

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestRingBuffer_AppendUnderCapacity(t *testing.T) {
	r := newRingBuffer(16)
	r.Append([]byte("hello"))
	if got, want := string(r.Bytes()), "hello"; got != want {
		t.Fatalf("Bytes=%q want %q", got, want)
	}
	r.Append([]byte(" world"))
	if got, want := string(r.Bytes()), "hello world"; got != want {
		t.Fatalf("Bytes=%q want %q", got, want)
	}
	if got := r.Len(); got != 11 {
		t.Fatalf("Len=%d want 11", got)
	}
}

func TestRingBuffer_EvictsOldestOnOverflow(t *testing.T) {
	r := newRingBuffer(8)
	r.Append([]byte("abcdefgh")) // exactly fills
	if got := string(r.Bytes()); got != "abcdefgh" {
		t.Fatalf("Bytes=%q want abcdefgh", got)
	}
	r.Append([]byte("ij")) // evicts "ab"
	if got := string(r.Bytes()); got != "cdefghij" {
		t.Fatalf("Bytes=%q want cdefghij", got)
	}
}

func TestRingBuffer_WriteLargerThanCapacityKeepsTail(t *testing.T) {
	r := newRingBuffer(4)
	r.Append([]byte("abcdefgh"))
	if got := string(r.Bytes()); got != "efgh" {
		t.Fatalf("Bytes=%q want efgh", got)
	}
	if got := r.Len(); got != 4 {
		t.Fatalf("Len=%d want 4", got)
	}
}

func TestRingBuffer_ZeroWriteNoop(t *testing.T) {
	r := newRingBuffer(8)
	r.Append(nil)
	r.Append([]byte{})
	if got := r.Len(); got != 0 {
		t.Fatalf("Len=%d want 0", got)
	}
}

func TestRingBuffer_DefaultCapacityWhenNonPositive(t *testing.T) {
	r := newRingBuffer(0)
	// Write one byte more than the default to force an evict and confirm the
	// cap is the documented default.
	big := bytes.Repeat([]byte{'x'}, defaultRingCapacity+1)
	r.Append(big)
	if got, want := r.Len(), defaultRingCapacity; got != want {
		t.Fatalf("Len=%d want %d", got, want)
	}
}

func TestRingBuffer_ConcurrentWritesDoNotRace(t *testing.T) {
	r := newRingBuffer(1024)
	var wg sync.WaitGroup
	payload := []byte(strings.Repeat("x", 16))
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Append(payload)
		}()
	}
	wg.Wait()
	// Can't assert exact contents due to interleaving, but length must be
	// capped at capacity and must be > 0.
	if got := r.Len(); got == 0 || got > 1024 {
		t.Fatalf("Len=%d outside expected range (0, 1024]", got)
	}
}

func TestRingBuffer_BytesReturnsCopy(t *testing.T) {
	r := newRingBuffer(16)
	r.Append([]byte("hello"))
	snap := r.Bytes()
	snap[0] = 'H'
	if got := string(r.Bytes()); got != "hello" {
		t.Fatalf("ring mutated externally; got %q want hello", got)
	}
}

func TestRingBuffer_ReplaySnapshotRestoresHeadModesAfterEviction(t *testing.T) {
	r := newRingBuffer(32)
	r.Append([]byte("\x1b[?1049h\x1b[?1003;1006;2004h"))
	r.Append(bytes.Repeat([]byte{'x'}, 32))

	checkpoint, body := r.ReplaySnapshot()
	for _, want := range []string{
		"\x1b[?1049h",
		"\x1b[?1003h",
		"\x1b[?1006h",
		"\x1b[?2004h",
	} {
		if !bytes.Contains(checkpoint, []byte(want)) {
			t.Errorf("checkpoint=%q missing %q", string(checkpoint), want)
		}
	}
	if !bytes.Equal(body, bytes.Repeat([]byte{'x'}, 32)) {
		t.Fatalf("body=%q want retained ring body", string(body))
	}
}

func TestRingBuffer_ReplaySnapshotCheckpointsDisabledModes(t *testing.T) {
	r := newRingBuffer(16)
	r.Append([]byte("\x1b[?1003h\x1b[?1003l"))
	r.Append(bytes.Repeat([]byte{'x'}, 16))

	checkpoint, _ := r.ReplaySnapshot()
	if bytes.Contains(checkpoint, []byte("\x1b[?1003h")) {
		t.Fatalf("checkpoint restored disabled mouse mode: %q", string(checkpoint))
	}
	if !bytes.Contains(checkpoint, []byte("\x1b[?1003l")) {
		t.Fatalf("checkpoint=%q missing explicit mouse-mode reset", string(checkpoint))
	}
}

func TestRingBuffer_ReplaySnapshotSetsSelectedMouseModesAfterPeerResets(t *testing.T) {
	r := newRingBuffer(16)
	r.Append([]byte("\x1b[?1000;1016h"))
	r.Append(bytes.Repeat([]byte{'x'}, 16))

	checkpoint, _ := r.ReplaySnapshot()
	cases := []struct {
		selected string
		resets   []string
	}{
		{"\x1b[?1000h", []string{"\x1b[?9l", "\x1b[?1002l", "\x1b[?1003l"}},
		{"\x1b[?1016h", []string{"\x1b[?1005l", "\x1b[?1006l", "\x1b[?1015l"}},
	}
	for _, tc := range cases {
		selectedAt := bytes.Index(checkpoint, []byte(tc.selected))
		if selectedAt < 0 {
			t.Fatalf("checkpoint=%q missing selected mode %q", string(checkpoint), tc.selected)
		}
		for _, reset := range tc.resets {
			if resetAt := bytes.Index(checkpoint, []byte(reset)); resetAt > selectedAt {
				t.Fatalf("checkpoint=%q emits peer reset %q after selected mode %q", string(checkpoint), reset, tc.selected)
			}
		}
	}
}

func TestRingBuffer_ReplaySnapshotLeavesTailTransitionsInOrder(t *testing.T) {
	r := newRingBuffer(24)
	r.Append([]byte("\x1b[?1049h"))
	r.Append(bytes.Repeat([]byte{'x'}, 24))
	r.Append([]byte("\x1b[?1049l"))

	checkpoint, body := r.ReplaySnapshot()
	if !bytes.Contains(checkpoint, []byte("\x1b[?1049h")) {
		t.Fatalf("checkpoint=%q missing head alternate-buffer state", string(checkpoint))
	}
	if !bytes.HasSuffix(body, []byte("\x1b[?1049l")) {
		t.Fatalf("body=%q missing retained mode transition", string(body))
	}
}

func TestRingBuffer_TracksPrivateModeSequenceAcrossChunks(t *testing.T) {
	r := newRingBuffer(16)
	r.Append([]byte("\x1b[?10"))
	r.Append([]byte("03;1006h"))
	r.Append(bytes.Repeat([]byte{'x'}, 16))

	checkpoint, _ := r.ReplaySnapshot()
	if !bytes.Contains(checkpoint, []byte("\x1b[?1003h")) ||
		!bytes.Contains(checkpoint, []byte("\x1b[?1006h")) {
		t.Fatalf("checkpoint=%q missing split mouse-mode state", string(checkpoint))
	}
}

func TestRingBuffer_EvictionDropsPrivateModeFragment(t *testing.T) {
	r := newRingBuffer(6)
	r.Append([]byte("abc\x1b[?1003h"))

	checkpoint, body := r.ReplaySnapshot()
	if !bytes.Contains(checkpoint, []byte("\x1b[?1003h")) {
		t.Fatalf("checkpoint=%q missing mode cut by eviction", string(checkpoint))
	}
	if bytes.Contains(body, []byte("1003h")) || bytes.HasPrefix(body, []byte("03h")) {
		t.Fatalf("body begins with an evicted control-sequence fragment: %q", string(body))
	}
}

func TestRingBuffer_ReplaySnapshotPlainShellUnchanged(t *testing.T) {
	r := newRingBuffer(16)
	r.Append([]byte("hello shell"))

	checkpoint, body := r.ReplaySnapshot()
	if len(checkpoint) != 0 {
		t.Fatalf("checkpoint=%q want empty for plain shell", string(checkpoint))
	}
	if got, want := string(body), "hello shell"; got != want {
		t.Fatalf("body=%q want %q", got, want)
	}
}

func TestRingBuffer_ReplaySnapshotIgnoresTransientPrivateModes(t *testing.T) {
	r := newRingBuffer(8)
	r.Append([]byte("\x1b[?2026h"))
	r.Append(bytes.Repeat([]byte{'x'}, 8))

	checkpoint, body := r.ReplaySnapshot()
	if len(checkpoint) != 0 {
		t.Fatalf("checkpoint=%q should not restore synchronized output", string(checkpoint))
	}
	if !bytes.Equal(body, bytes.Repeat([]byte{'x'}, 8)) {
		t.Fatalf("body=%q want retained bytes", string(body))
	}
}

func TestPtySessionAttachReplayRestoresModesBeforeClearingScreen(t *testing.T) {
	scrollback := newRingBuffer(16)
	scrollback.Append([]byte("\x1b[?1049h\x1b[?1003;1006h"))
	scrollback.Append(bytes.Repeat([]byte{'x'}, 16))

	replay := interimInitialStateData(scrollback)
	modeAt := bytes.Index(replay, []byte("\x1b[?1049h"))
	clearAt := bytes.Index(replay, screenResetSeq)
	if modeAt < 0 || clearAt < 0 || modeAt > clearAt {
		t.Fatalf("replay=%q must restore alternate screen before clear/home", string(replay))
	}
}
