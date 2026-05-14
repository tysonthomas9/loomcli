package data

import (
	"context"
	"fmt"
	"net/http"
	"os"
)

// resolveWorkspaceID determines the target workspace ID from LOOM_WORKSPACE.
// The root --workspace flag mirrors into this env var in PersistentPreRunE
// so both `loom --workspace X data list` and `LOOM_WORKSPACE=X loom data list`
// flow through the same path.
func resolveWorkspaceID(_ context.Context, _ *http.Client, _ string) (string, error) {
	if v := os.Getenv("LOOM_WORKSPACE"); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("workspace is required; specify --workspace or LOOM_WORKSPACE")
}
