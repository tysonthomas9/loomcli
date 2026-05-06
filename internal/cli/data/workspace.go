package data

import (
	"context"
	"fmt"
	"net/http"
	"os"
)

// resolveWorkspaceID determines the target workspace ID using the standard
// precedence: --workspace flag > LOOM_WORKSPACE env var.
func resolveWorkspaceID(_ context.Context, _ *http.Client, _ string) (string, error) {
	if workspaceID != "" {
		return workspaceID, nil
	}
	if v := os.Getenv("LOOM_WORKSPACE"); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("workspace is required; specify --workspace or LOOM_WORKSPACE")
}
