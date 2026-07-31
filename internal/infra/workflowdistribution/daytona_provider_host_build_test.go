package workflowdistribution

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestDaytonaProviderHostBundleBuild is an opt-in integration guard against
// drift in the pinned Flue/Valibot toolchain. It builds the real embedded bundle
// but does not contact Daytona or run a model.
func TestDaytonaProviderHostBundleBuild(t *testing.T) {
	if os.Getenv("LOOM_DAYTONA_PROVIDER_BUNDLE_TEST") != "1" {
		t.Skip("set LOOM_DAYTONA_PROVIDER_BUNDLE_TEST=1 with the pinned Flue checkout")
	}
	serverPath, diagnostics, err := BuildBuiltinBundle(
		context.Background(),
		BuiltinEpicRunnerWorkflowName,
		filepath.Join(t.TempDir(), "dist"),
	)
	if err != nil {
		t.Fatalf("build Daytona provider host bundle: %v\n%s", err, diagnostics)
	}
	if _, err := os.Stat(serverPath); err != nil {
		t.Fatalf("built Daytona provider host server missing: %v", err)
	}
}
