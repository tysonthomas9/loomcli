package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBus_EmitWritesAndNotifies(t *testing.T) {
	dir := t.TempDir()
	bus := NewBus(t.Context(), dir)
	defer bus.Close()

	var received Event
	bus.Subscribe(func(e Event) {
		received = e
	})

	e, _ := NewEvent(TaskClaimed, "a1", "task", "epic1", TaskClaimedData{TaskID: "t1", Title: "Test"})
	e.Timestamp = time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	if err := bus.Emit(e); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	bus.Close()

	// Verify listener was called
	if received.Type != TaskClaimed {
		t.Errorf("listener got Type=%q, want task_claimed", received.Type)
	}

	// Verify written to file
	path := filepath.Join(dir, "events-2026-03-04.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var written Event
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if written.Type != TaskClaimed {
		t.Errorf("written Type=%q", written.Type)
	}
}

func TestBus_AutoSetsTimestamp(t *testing.T) {
	dir := t.TempDir()
	bus := NewBus(t.Context(), dir)
	defer bus.Close()

	fixed := time.Date(2026, 3, 4, 15, 30, 0, 0, time.UTC)
	origNow := Now
	Now = func() time.Time { return fixed }
	defer func() { Now = origNow }()

	var received Event
	bus.Subscribe(func(e Event) {
		received = e
	})

	// NewEvent now sets Timestamp via Now(), so this will get the fixed time.
	// Verify Bus.Emit preserves it (doesn't overwrite non-zero timestamps).
	e, _ := NewEvent(AgentStarted, "a1", "task", "", AgentStartedData{PID: 42})
	if err := bus.Emit(e); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	if !received.Timestamp.Equal(fixed) {
		t.Errorf("Timestamp = %v, want %v", received.Timestamp, fixed)
	}

	// Also verify that Emit sets timestamp when explicitly zeroed
	e2, _ := NewEvent(AgentStopped, "a1", "task", "", AgentStoppedData{PID: 42, ExitCode: 0})
	e2.Timestamp = time.Time{} // zero it out
	if err := bus.Emit(e2); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if !received.Timestamp.Equal(fixed) {
		t.Errorf("zeroed Timestamp not auto-set: %v", received.Timestamp)
	}
}

func TestBus_MultipleListeners(t *testing.T) {
	dir := t.TempDir()
	bus := NewBus(t.Context(), dir)
	defer bus.Close()

	var count int32
	for i := 0; i < 5; i++ {
		bus.Subscribe(func(e Event) {
			atomic.AddInt32(&count, 1)
		})
	}

	e, _ := NewEvent(HealthCheck, "", "", "", HealthCheckData{AgentCount: 3, HealthyCount: 3})
	e.Timestamp = time.Now()
	if err := bus.Emit(e); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	if c := atomic.LoadInt32(&count); c != 5 {
		t.Errorf("listener count = %d, want 5", c)
	}
}

func TestBus_ConcurrentEmit(t *testing.T) {
	dir := t.TempDir()
	bus := NewBus(t.Context(), dir)
	defer bus.Close()

	var count int64
	bus.Subscribe(func(e Event) {
		atomic.AddInt64(&count, 1)
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e, _ := NewEvent(TaskStarted, "a1", "task", "", TaskStartedData{TaskID: "t1"})
			e.Timestamp = time.Now()
			bus.Emit(e)
		}()
	}
	wg.Wait()

	if c := atomic.LoadInt64(&count); c != 50 {
		t.Errorf("count = %d, want 50", c)
	}
}

func TestNopBus_DiscardsSilently(t *testing.T) {
	var bus Emitter = NopBus{}
	e, _ := NewEvent(TaskClaimed, "a1", "task", "", TaskClaimedData{TaskID: "t1"})
	if err := bus.Emit(e); err != nil {
		t.Errorf("NopBus.Emit should not error: %v", err)
	}
	if err := bus.Close(); err != nil {
		t.Errorf("NopBus.Close should not error: %v", err)
	}
}
