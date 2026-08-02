package serve

import (
	"testing"
	"time"
)

// TestDriverStaleTaskMaxAge_UnsetDefersToSweeperDefault pins the fix for the
// shadowed 20-minute sweeper default: with the env var unset this MUST return
// 0 so StaleTaskSweeper.maxAge() falls through to
// driver.defaultStaleTaskRunMaxAge. The old serve-side default (300s) always
// won the MaxAge > 0 preference and re-created the exact live-run sweep the
// 20-minute constant was raised to prevent.
func TestDriverStaleTaskMaxAge_UnsetDefersToSweeperDefault(t *testing.T) {
	t.Setenv(envLoomDriverStaleTaskMaxAge, "")
	if got := driverStaleTaskMaxAge(); got != 0 {
		t.Fatalf("driverStaleTaskMaxAge() with unset env = %v, want 0 (defer to sweeper default)", got)
	}
}

func TestDriverStaleTaskMaxAge_ExplicitValueWins(t *testing.T) {
	t.Setenv(envLoomDriverStaleTaskMaxAge, "600")
	if got := driverStaleTaskMaxAge(); got != 600*time.Second {
		t.Fatalf("driverStaleTaskMaxAge() = %v, want 600s", got)
	}
}
