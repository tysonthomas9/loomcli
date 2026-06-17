package workflows

import (
	"os"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/runtimepreflight"
)

// TestMain installs a healthy backend health stub for the whole package so the
// epic-runner POST tests (which default to the local task runner) are not gated
// by whatever backend CLI/auth happens to exist on the test host. Tests that
// specifically exercise the fail-closed preflight override this per-test via
// runtimepreflight.SetHealthCheckerForTest.
func TestMain(m *testing.M) {
	restore := runtimepreflight.SetHealthCheckerForTest(func(string) (backends.HealthStatus, bool) {
		return backends.HealthStatus{Healthy: true, Installed: true, APIKeySet: true, Message: "ready"}, true
	})
	code := m.Run()
	restore()
	os.Exit(code)
}
