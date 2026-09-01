package supervisor

import (
	"testing"
	"time"
)

func TestEmitLogHeartbeat_Throttles(t *testing.T) {
	s := &Supervisor{}

	s.emitLogHeartbeat(1, 1)
	first := s.lastHeartbeat
	if first.IsZero() {
		t.Fatal("first call did not emit a heartbeat")
	}

	// Inside the default window: suppressed.
	s.emitLogHeartbeat(1, 1)
	if !s.lastHeartbeat.Equal(first) {
		t.Error("heartbeat emitted twice inside one interval")
	}

	// Past the window: emitted again.
	s.lastHeartbeat = time.Now().Add(-2 * defaultLogHeartbeatInterval)
	s.emitLogHeartbeat(1, 1)
	if !s.lastHeartbeat.After(first) {
		t.Error("heartbeat not emitted after the interval elapsed")
	}
}

func TestEmitLogHeartbeat_NegativeIntervalDisables(t *testing.T) {
	s := &Supervisor{LogHeartbeatInterval: -1}
	s.emitLogHeartbeat(1, 1)
	if !s.lastHeartbeat.IsZero() {
		t.Error("heartbeat emitted despite being disabled")
	}
}
