package data

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
)

var monitorCmd = &cobra.Command{
	Use:   "monitor",
	Short: "Show monitor dashboard from a loom server (HTTP, single-shot)",
	Long: `Fetch /api/monitor/status from the configured loom server and render
a simplified dashboard. This command is single-shot — it does not live-refresh
or draw terminal boxes.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		cli, url, err := getHTTPClient()
		if err != nil {
			return err
		}
		status, err := fetchMonitorStatus(ctx, cli, url)
		if err != nil {
			return err
		}
		return printMonitorStatus(os.Stdout, status, outputFormat)
	},
}

// fetchMonitorStatus performs a single GET to /api/monitor/status and
// decodes the response. The monitor endpoint does NOT use the
// {success,data,error} envelope that api.APIBackend's doRequest expects —
// it returns MonitorStatusResponse directly — so we inline a small helper
// here instead of reusing the backend's exec loop.
func fetchMonitorStatus(ctx context.Context, cli *http.Client, baseURL string) (*gen.MonitorStatusResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/monitor/status", nil)
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
