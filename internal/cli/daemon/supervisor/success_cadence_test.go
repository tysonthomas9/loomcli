package supervisor

import (
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// The floor is the ONLY cadence bound on a success loop (clean exits reset
// every other budget — the 11-billed-turns-in-45s incident). These pin its
// three behaviors: fresh success waits the floor, a run longer than the floor
// pays nothing, and 0 disables it.
func TestComputeBackoff_SuccessCadenceFloor(t *testing.T) {
	s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{} }}
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "w", Role: "task"}}

	t.Run("fresh success waits out the floor", func(t *testing.T) {
		t.Setenv(envSuccessCadenceSeconds, "10")
		ap.Mu.Lock()
		ap.LastStart = time.Now()
		ap.LastError = nil
		ap.Mu.Unlock()
		got := s.computeBackoff(ap)
		if got < 8*time.Second || got > 10*time.Second {
			t.Fatalf("backoff = %v, want ~10s cadence remainder", got)
		}
	})

	t.Run("a run longer than the floor pays nothing", func(t *testing.T) {
		t.Setenv(envSuccessCadenceSeconds, "10")
		ap.Mu.Lock()
		ap.LastStart = time.Now().Add(-time.Minute)
		ap.Mu.Unlock()
		if got := s.computeBackoff(ap); got != 0 {
			t.Fatalf("backoff = %v, want 0 after a long run", got)
		}
	})

	t.Run("zero disables the floor", func(t *testing.T) {
		t.Setenv(envSuccessCadenceSeconds, "0")
		ap.Mu.Lock()
		ap.LastStart = time.Now()
		ap.Mu.Unlock()
		if got := s.computeBackoff(ap); got != 0 {
			t.Fatalf("backoff = %v, want 0 when disabled", got)
		}
	})

	t.Run("default floor applies without the env", func(t *testing.T) {
		ap.Mu.Lock()
		ap.LastStart = time.Now()
		ap.Mu.Unlock()
		got := s.computeBackoff(ap)
		if got < 3*time.Second || got > 5*time.Second {
			t.Fatalf("backoff = %v, want ~%ds default cadence", got, defaultSuccessCadenceSeconds)
		}
	})
}
