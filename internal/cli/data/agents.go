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

	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
)

// agentsListEnvelope matches dto.ListResponse[gen.AgentControlEntry]
// returned by GET /api/workspaces/{ws}/agents. We inline the shape here
// rather than adding a shared decoder because it is used in exactly one
// place and inlining keeps cli/data flat.
type agentsListEnvelope struct {
	Success bool                    `json:"success"`
	Data    []gen.AgentControlEntry `json:"data"`
	Total   int                     `json:"total"`
	Error   string                  `json:"error,omitempty"`
}

var agentsCmd = &cobra.Command{
	Use:   "agents",
	Short: "List agents managed by the loom server daemon (HTTP)",
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
// dto.ListResponse envelope into a slice of AgentControlEntry.
func fetchAgents(ctx context.Context, cli *http.Client, baseURL, wsID string) ([]gen.AgentControlEntry, error) {
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
		return nil, fmt.Errorf("daemon unavailable on server")
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
