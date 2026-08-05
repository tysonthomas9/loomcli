package data

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/spf13/cobra"
)

type agentListBehavior struct {
	RoleName        string `json:"role_name,omitempty"`
	DriverID        string `json:"driver_id,omitempty"`
	DriverVersionID string `json:"driver_version_id,omitempty"`
}

// agentListEntry is the canonical Agents collection projection consumed by
// the data CLI. It deliberately does not reuse the generated oneOf wrapper,
// which is awkward for a flat human-readable list and previously hid the
// canonical nested behavior behind a retired daemon-control DTO.
type agentListEntry struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Kind         string            `json:"kind"`
	Enabled      bool              `json:"enabled"`
	Behavior     agentListBehavior `json:"behavior"`
	WorkspaceKey string            `json:"workspace_key"`
}

// agentsListEnvelope matches the canonical collection response returned by
// GET /api/workspaces/{ws}/agents.
type agentsListEnvelope struct {
	Success bool             `json:"success"`
	Data    []agentListEntry `json:"data"`
	Total   int              `json:"total"`
	Error   string           `json:"error,omitempty"`
}

var agentsCmd = &cobra.Command{
	Use:   "agents",
	Short: "List agents managed by the Loom platform runtime (HTTP)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		cli, baseURL, err := getHTTPClient()
		if err != nil {
			return err
		}
		wsID, err := resolveWorkspaceID(ctx, cli, baseURL)
		if err != nil {
			return err
		}
		entries, err := fetchAgents(ctx, cli, baseURL, wsID)
		if err != nil {
			return err
		}
		return printAgentList(os.Stdout, entries, outputFormat)
	},
}

// fetchAgents issues GET /api/workspaces/{ws}/agents and unwraps the
// canonical list envelope into its compact CLI projection.
func fetchAgents(ctx context.Context, cli *http.Client, baseURL, wsID string) ([]agentListEntry, error) {
	path := baseURL + "/api/workspaces/" + url.PathEscape(wsID) + "/agents"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusServiceUnavailable {
		return nil, fmt.Errorf("agent runtime unavailable on server")
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil, fmt.Errorf("server returned no body (HTTP 204)")
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("agents: HTTP %d", resp.StatusCode)
	}

	// Agent list payloads are small; bound to 1 MiB.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read agents response: %w", err)
	}
	var env agentsListEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode agents response: %w", err)
	}
	if !env.Success && env.Error != "" {
		return nil, fmt.Errorf("agents: %s", env.Error)
	}
	return env.Data, nil
}
