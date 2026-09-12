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

var monitorCmd = &cobra.Command{
	Use:   "monitor",
	Short: "Show monitor dashboard from a loom server (HTTP, single-shot)",
	Long: `Fetch the monitor status for a workspace from the configured loom
server and render a simplified dashboard. This command is single-shot — it does
not live-refresh or draw terminal boxes.

A workspace is required: monitor collection is workspace-scoped on the server,
so an unscoped request returns an empty dashboard rather than the fleet's real
state. Specify --workspace or set LOOM_WORKSPACE.`,
	Args: cobra.NoArgs,
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
		status, err := fetchMonitorStatus(ctx, cli, baseURL, wsID)
		if err != nil {
			return err
		}
		return printMonitorStatus(os.Stdout, status, outputFormat)
	},
}

// fetchMonitorStatus performs a single GET to the workspace-scoped
// /api/workspaces/{ws}/monitor/status and decodes the response.
//
// The scoped route matters: the server's monitor collection is per workspace,
// and the unscoped /api/monitor/status falls through to a workspace-less
// collector that reports zeros for every count. The monitor endpoint does NOT
// use the {success,data,error} envelope that api.APIBackend's doRequest
// expects — it returns MonitorStatusResponse directly — so we inline a small
// helper here instead of reusing the backend's exec loop.
func fetchMonitorStatus(ctx context.Context, cli *http.Client, baseURL, wsID string) (*gen.MonitorStatusResponse, error) {
	endpoint := baseURL + "/api/workspaces/" + url.PathEscape(wsID) + "/monitor/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
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
		return nil, fmt.Errorf("monitor data unavailable on server")
	}
	// Name the URL: a 404 here is either an unknown workspace or a server
	// binary predating the scoped route, and the two are worth telling apart.
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("workspace %q not found on server (GET %s returned 404); "+
			"check the workspace name, or the server may predate the scoped monitor route", wsID, endpoint)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("monitor: HTTP %d", resp.StatusCode)
	}

	// Monitor status payloads are small (agent metadata + counts). Cap at
	// 4 MiB to bound client memory on a pathological/malicious server.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read monitor response: %w", err)
	}
	var out gen.MonitorStatusResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode monitor response: %w", err)
	}
	return &out, nil
}
