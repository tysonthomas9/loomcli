package serve

import (
	"testing"
	"time"

	driverexecutor "github.com/tysonthomas9/loomcli/internal/driver"
)

// TestDriverStaleTaskMaxAge_UnsetDefersToSweeperDefault pins the fix for the
// shadowed 20-minute sweeper default: with the env var unset this MUST resolve
// to driver.DefaultStaleTaskRunMaxAge rather than a serve-local constant. The
// old serve-side default (300s) always won the MaxAge > 0 preference and
// re-created the exact live-run sweep the 20-minute constant was raised to
// prevent. serve now sources that constant directly, so the value it passes is
// the sweeper default instead of a number that has to be kept in sync with it.
func TestDriverStaleTaskMaxAge_UnsetDefersToSweeperDefault(t *testing.T) {
	t.Setenv(envLoomDriverStaleTaskMaxAge, "")
	if got := driverStaleTaskMaxAge(); got != driverexecutor.DefaultStaleTaskRunMaxAge {
		t.Fatalf("driverStaleTaskMaxAge() with unset env = %v, want the sweeper default %v", got, driverexecutor.DefaultStaleTaskRunMaxAge)
	}
}

func TestDriverStaleTaskMaxAge_ExplicitValueWins(t *testing.T) {
	t.Setenv(envLoomDriverStaleTaskMaxAge, "600")
	if got := driverStaleTaskMaxAge(); got != 600*time.Second {
		t.Fatalf("driverStaleTaskMaxAge() = %v, want 600s", got)
	}
}
