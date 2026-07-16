package driverapi

import (
	"os"
	"testing"

	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
)

// TestMain enables the test-only noop provider gate (§4.5) for the driverapi
// test binary so the exec-task HTTP tests that drive the local-noop provider
// reach their input-persistence / scheduling assertions instead of failing
// closed at the provider preflight (provider_unsupported). This mirrors the
// driver package's own opt-in; production never sets the var.
func TestMain(m *testing.M) {
	_ = os.Setenv(driverpkg.NoopTaskProviderEnvVar, "1")
	os.Exit(m.Run())
}
