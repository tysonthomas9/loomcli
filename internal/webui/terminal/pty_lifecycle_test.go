package terminal

import (
	"sync"
	"testing"
	"time"
)

type recordingPTYLifecycleObserver struct {
	mu     sync.Mutex
	events []PTYLifecycleEvent
	wake   chan struct{}
}

func newRecordingPTYLifecycleObserver() *recordingPTYLifecycleObserver {
	return &recordingPTYLifecycleObserver{wake: make(chan struct{}, 16)}
}

func (o *recordingPTYLifecycleObserver) OnPTYLifecycle(event PTYLifecycleEvent) {
	o.mu.Lock()
	o.events = append(o.events, event)
	o.mu.Unlock()
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

func (o *recordingPTYLifecycleObserver) snapshot() []PTYLifecycleEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]PTYLifecycleEvent(nil), o.events...)
}

func waitForLifecycleAction(t *testing.T, observer *recordingPTYLifecycleObserver, action string) PTYLifecycleEvent {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		for _, event := range observer.snapshot() {
			if event.Action == action {
				return event
			}
		}
		select {
		case <-observer.wake:
		case <-deadline.C:
			t.Fatalf("timed out waiting for lifecycle action %q; events=%+v", action, observer.snapshot())
		}
	}
}

func TestPTYLifecycleObserverEmitsStartedOnSpawn(t *testing.T) {
	observer := newRecordingPTYLifecycleObserver()
	m := NewPTYManager("cat", 0, t.TempDir(), observer)
	t.Cleanup(func() { _ = m.Shutdown() })
	key := SessionKey{Workspace: "ws-1", Name: "shell-1"}

	if _, reattached, err := m.AttachSession(key, 80, 24, nil); err != nil {
		t.Fatalf("AttachSession: %v", err)
	} else if reattached {
		t.Fatal("first AttachSession unexpectedly reported reattach")
	}

	event := waitForLifecycleAction(t, observer, PTYLifecycleStarted)
	if event.Key != key || !event.PTYAlive || event.ExitReason != "" || event.Kind != PTYKind || event.Agent {
		t.Fatalf("started event = %+v", event)
	}
}

func TestPTYLifecycleObserverEmitsExitedOnNaturalExit(t *testing.T) {
	observer := newRecordingPTYLifecycleObserver()
	m := NewPTYManager("read line", 0, t.TempDir(), observer)
	t.Cleanup(func() { _ = m.Shutdown() })
	key := SessionKey{Workspace: "ws-1", Name: "natural-exit"}

	att, _, err := m.AttachSession(key, 80, 24, nil)
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	if _, err := att.WriteInput([]byte("done\n")); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}

	event := waitForLifecycleAction(t, observer, PTYLifecycleExited)
	if event.Key != key || event.PTYAlive || event.ExitReason != ExitReasonExited {
		t.Fatalf("exited event = %+v", event)
	}
}

func TestPTYLifecycleObserverEmitsKilledOnKill(t *testing.T) {
	observer := newRecordingPTYLifecycleObserver()
	m := NewPTYManager("cat", 0, t.TempDir(), observer)
	t.Cleanup(func() { _ = m.Shutdown() })
	key := SessionKey{Workspace: "ws-1", Name: "killed"}

	if _, _, err := m.AttachSession(key, 80, 24, nil); err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	if err := m.Kill(key); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	event := waitForLifecycleAction(t, observer, PTYLifecycleKilled)
	if event.Key != key || event.PTYAlive || event.ExitReason != ExitReasonKilled {
		t.Fatalf("killed event = %+v", event)
	}
}

func TestPTYLifecycleObserverDoesNotEmitStartedOnReattach(t *testing.T) {
	observer := newRecordingPTYLifecycleObserver()
	m := NewPTYManager("cat", 0, t.TempDir(), observer)
	t.Cleanup(func() { _ = m.Shutdown() })
	key := SessionKey{Workspace: "ws-1", Name: "reattach"}

	if _, _, err := m.AttachSession(key, 80, 24, nil); err != nil {
		t.Fatalf("first AttachSession: %v", err)
	}
	_ = waitForLifecycleAction(t, observer, PTYLifecycleStarted)
	if _, reattached, err := m.AttachSession(key, 80, 24, nil); err != nil {
		t.Fatalf("second AttachSession: %v", err)
	} else if !reattached {
		t.Fatal("second AttachSession did not report reattach")
	}

	started := 0
	for _, event := range observer.snapshot() {
		if event.Action == PTYLifecycleStarted {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("started event count = %d, want 1; events=%+v", started, observer.snapshot())
	}
}

func TestMultiPTYManagerForwardsLifecycleObserver(t *testing.T) {
	observer := newRecordingPTYLifecycleObserver()
	manager := NewMultiPTYManager("cat", 0, observer)
	t.Cleanup(func() { _ = manager.Close() })
	if err := manager.Register("ws-multi", t.TempDir()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	key := SessionKey{Workspace: "ws-multi", Name: "shell-multi"}

	if _, _, err := manager.AttachSession(key, 80, 24, nil); err != nil {
		t.Fatalf("AttachSession: %v", err)
	}

	event := waitForLifecycleAction(t, observer, PTYLifecycleStarted)
	if event.Key != key {
		t.Fatalf("event key = %+v, want %+v", event.Key, key)
	}
}
