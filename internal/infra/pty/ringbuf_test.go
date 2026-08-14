package pty

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestReplayBufferPreservesOutputResizeOrdering(t *testing.T) {
	buffer := newReplayBuffer(64)
	buffer.AppendResize(80, 24)
	buffer.AppendOutput([]byte("hello"))
	buffer.AppendOutput([]byte(" world"))
	buffer.AppendResize(132, 40)
	buffer.AppendOutput([]byte("after"))

	events := buffer.Snapshot()
	if len(events) != 4 {
		t.Fatalf("events = %#v", events)
	}
	if !events[0].IsResize() || events[0].Columns != 80 || events[0].Rows != 24 {
		t.Fatalf("initial resize = %#v", events[0])
	}
	if got := string(events[1].Output); got != "hello world" {
		t.Fatalf("first output = %q", got)
	}
	if !events[2].IsResize() || events[2].Columns != 132 || events[2].Rows != 40 {
		t.Fatalf("second resize = %#v", events[2])
	}
	if got := string(events[3].Output); got != "after" {
		t.Fatalf("second output = %q", got)
	}
}

func TestReplayBufferEvictsWholeHistoryPrefix(t *testing.T) {
	buffer := newReplayBuffer(16)
	buffer.AppendResize(80, 24)
	buffer.AppendOutput([]byte("abcdefgh"))
	buffer.AppendResize(120, 40)
	buffer.AppendOutput([]byte("ijklmnop"))

	events := buffer.Snapshot()
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
	if !events[0].IsResize() || events[0].Columns != 120 {
		t.Fatalf("oldest retained event = %#v", events[0])
	}
	if got := string(events[1].Output); got != "ijklmnop" {
		t.Fatalf("retained output = %q", got)
	}
}

func TestReplayBufferWriteLargerThanCapacityKeepsTail(t *testing.T) {
	buffer := newReplayBuffer(4)
	buffer.AppendOutput([]byte("abcdefgh"))
	events := buffer.Snapshot()
	if len(events) != 1 || string(events[0].Output) != "efgh" {
		t.Fatalf("events = %#v", events)
	}
	if got := buffer.Len(); got != 4 {
		t.Fatalf("Len=%d want 4", got)
	}
}

func TestReplayBufferRetainsGeometryForTrimmedOutput(t *testing.T) {
	buffer := newReplayBuffer(16)
	buffer.AppendResize(80, 24)
	buffer.AppendOutput([]byte("abcdefghijklmnop"))

	events := buffer.Snapshot()
	if len(events) != 2 || !events[0].IsResize() ||
		events[0].Columns != 80 || events[0].Rows != 24 ||
		string(events[1].Output) != "ijklmnop" {
		t.Fatalf("events = %#v", events)
	}
	if got := buffer.Len(); got != 16 {
		t.Fatalf("Len=%d want 16", got)
	}
}

func TestReplayBufferZeroWritesAreNoop(t *testing.T) {
	buffer := newReplayBuffer(8)
	buffer.AppendOutput(nil)
	buffer.AppendOutput([]byte{})
	buffer.AppendResize(0, 24)
	if got := buffer.Len(); got != 0 {
		t.Fatalf("Len=%d want 0", got)
	}
}

func TestReplayBufferDefaultCapacityWhenNonPositive(t *testing.T) {
	buffer := newReplayBuffer(0)
	buffer.AppendOutput(bytes.Repeat([]byte{'x'}, defaultRingCapacity+1))
	if got, want := buffer.Len(), defaultRingCapacity; got != want {
		t.Fatalf("Len=%d want %d", got, want)
	}
}

func TestReplayBufferConcurrentWritesDoNotRace(t *testing.T) {
	buffer := newReplayBuffer(1024)
	var wait sync.WaitGroup
	payload := []byte(strings.Repeat("x", 16))
	for range 100 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			buffer.AppendOutput(payload)
		}()
	}
	wait.Wait()
	if got := buffer.Len(); got == 0 || got > 1024 {
		t.Fatalf("Len=%d outside expected range (0, 1024]", got)
	}
}

func TestReplayBufferSnapshotReturnsCopy(t *testing.T) {
	buffer := newReplayBuffer(16)
	buffer.AppendOutput([]byte("hello"))
	snapshot := buffer.Snapshot()
	snapshot[0].Output[0] = 'H'
	if got := string(buffer.Snapshot()[0].Output); got != "hello" {
		t.Fatalf("buffer mutated externally; got %q want hello", got)
	}
}
