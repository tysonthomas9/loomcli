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
