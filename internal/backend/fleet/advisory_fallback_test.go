package fleet

import (
	"net/http"
	"testing"
	"time"
)

func TestIsNoRoleDenial(t *testing.T) {
	tests := []struct {
		name   string
		status int
		resp   *apiResponse
		want   bool
	}{
		{
			name:   "no-role denial",
			status: http.StatusForbidden,
			resp:   &apiResponse{Code: "forbidden", Error: "workspace access denied"},
			want:   true,
		},
		{
			// The actor HAS a role; it just lacks the permission.
			// Retrying as the admin process actor would be escalation.
			name:   "insufficient permissions is not retryable",
			status: http.StatusForbidden,
			resp:   &apiResponse{Code: "forbidden", Error: "insufficient permissions"},
		},
		{
			name:   "await forbidden is a different code",
			status: http.StatusForbidden,
			resp:   &apiResponse{Code: "await_actor_forbidden", Error: "workspace access denied"},
		},
		{
			name:   "401 is a real auth failure",
			status: http.StatusUnauthorized,
			resp:   &apiResponse{Code: "forbidden", Error: "workspace access denied"},
		},
		{
			name:   "success",
			status: http.StatusOK,
			resp:   &apiResponse{Success: true},
		},
		{name: "nil response", status: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNoRoleDenial(tt.status, tt.resp); got != tt.want {
				t.Errorf("isNoRoleDenial = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAdvisoryDenialCache_TTLAndEviction(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	b := &FleetBackend{workspace: "test-ws", now: func() time.Time { return now }}

	if b.advisoryActorDenied("operator@local") {
		t.Fatal("unrecorded actor reported as denied")
	}

	b.recordAdvisoryDenial("operator@local", "process@local")
	if !b.advisoryActorDenied("operator@local") {
		t.Fatal("recorded actor not reported as denied")
	}
	if b.advisoryActorDenied("someone-else@local") {
		t.Error("denial leaked to a different actor")
	}

	// Just inside the window: still denied.
	now = now.Add(advisoryDenialTTL - time.Second)
	if !b.advisoryActorDenied("operator@local") {
		t.Error("denial expired early")
	}

	// Past the window: the actor is probed again, so a role granted in the
	// meantime takes effect without a restart.
	now = now.Add(2 * time.Second)
	if b.advisoryActorDenied("operator@local") {
		t.Error("denial outlived its TTL")
	}

	// Recording a second actor evicts the expired entry.
	b.recordAdvisoryDenial("other@local", "process@local")
	b.mu.RLock()
	_, stale := b.deniedActors["operator@local"]
	size := len(b.deniedActors)
	b.mu.RUnlock()
	if stale {
		t.Error("expired entry was not evicted")
	}
	if size != 1 {
		t.Errorf("cache size = %d, want 1", size)
	}
}

func TestClock_DefaultsToWallClock(t *testing.T) {
	b := &FleetBackend{}
	if b.clock().IsZero() {
		t.Error("clock() on a zero-value backend returned the zero time")
	}
}
