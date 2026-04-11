package data

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// resolveWorkspaceID determines the target workspace ID using the standard
// precedence: --workspace flag > LOOM_WORKSPACE env var > server discovery
// via GET {baseURL}/api/workspaces/active.
//
// The logic duplicates discoverActiveWorkspace() in internal/cli — the
// duplication is deliberate because cli/data must not import cli. The
// duplicated surface is ~20 lines and the server contract is fixed by the
// OpenAPI spec, so drift is not a concern.
func resolveWorkspaceID(ctx context.Context, client *http.Client, baseURL string) (string, error) {
	if workspaceID != "" {
		return workspaceID, nil
	}
	if v := os.Getenv("LOOM_WORKSPACE"); v != "" {
		return v, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/workspaces/active", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("no active workspace on server; specify --workspace <id>")
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("discover active workspace: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read active workspace response: %w", err)
	}
	var ws struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &ws); err != nil {
		return "", fmt.Errorf("parse active workspace response: %w", err)
	}
	if ws.ID == "" {
		return "", fmt.Errorf("server returned active workspace with empty id; specify --workspace <id>")
	}
	return ws.ID, nil
}
