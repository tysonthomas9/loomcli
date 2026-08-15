package fleetdb_test

import (
	"os"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
)

const (
	envRunEmbeddedSmoke     = "LOOM_RUN_EMBEDDED_SMOKE"
	envRequireEmbeddedSmoke = "LOOM_REQUIRE_EMBEDDED_SMOKE"
)

// requireEmbeddedFleetDBConformance preserves the opt-in skip used by ordinary
// development runs while making the dedicated conformance lane fail closed.
func requireEmbeddedFleetDBConformance(t testing.TB) {
	t.Helper()
	required := os.Getenv(envRequireEmbeddedSmoke) == "1"
	if os.Getenv(envRunEmbeddedSmoke) != "1" {
		if required {
			t.Fatalf("%s=1 requires %s=1", envRequireEmbeddedSmoke, envRunEmbeddedSmoke)
		}
		t.Skipf("set %s=1 (with a freshly built fleet-db binary) to run fleet-db conformance", envRunEmbeddedSmoke)
	}
	if diag := bootstrap.DiagnoseFleetDBBinary(); diag.Err != nil {
		if required {
			t.Fatalf("fleet-db binary required by %s=1 but unavailable: %v", envRequireEmbeddedSmoke, diag.Err)
		}
		t.Skipf("fleet-db binary unavailable: %v", diag.Err)
	}
}
