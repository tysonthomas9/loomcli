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

// agentMessageEnvelope matches the generated MessageResponse contract returned by the agent
// control endpoints.
type agentMessageEnvelope struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// agentCmd is the `loom data agent` parent command (manage individual
// agents by name).
var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Control a single agent on the Loom server (stop/start/restart)",
}

var agentStopCmd = &cobra.Command{
	Use:   "stop <agent-name>",
	Short: "Stop an agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgentControl(cmd.Context(), args[0], "stop")
	},
}

var agentStartCmd = &cobra.Command{
	Use:   "start <agent-name>",
	Short: "Start a stopped agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgentControl(cmd.Context(), args[0], "start")
	},
}

var agentRestartCmd = &cobra.Command{
	Use:   "restart <agent-name>",
	Short: "Restart an agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgentControl(cmd.Context(), args[0], "restart")
	},
}

func init() {
	agentCmd.AddCommand(agentStopCmd, agentStartCmd, agentRestartCmd)
}

// runAgentControl POSTs to /api/workspaces/{ws}/agents/{name}/{action}.
func runAgentControl(ctx context.Context, name, action string) error {
	cli, baseURL, err := getHTTPClient()
	if err != nil {
		return err
	}
	wsID, err := resolveWorkspaceID(ctx, cli, baseURL)
	if err != nil {
		return err
	}

	path := baseURL + "/api/workspaces/" + url.PathEscape(wsID) + "/agents/" + url.PathEscape(name) + "/" + url.PathEscape(action)
	raw, err := postAgentAction(ctx, cli, path, action)
	if err != nil {
		return err
	}

	msg, err := decodeAgentMessage(raw, action)
	if err != nil {
		return err
	}
	if msg == "" {
		msg = fmt.Sprintf("agent %q %s", name, action)
	}
	return printMessageResult(os.Stdout, msg, outputFormat)
}

// postAgentAction sends the canonical lifecycle POST and requires a settled
// 200 response. The retired daemon command path used 202; accepting it here
// would hide a server/client version mismatch.
func postAgentAction(ctx context.Context, cli *http.Client, path, action string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, nil)
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
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent %s: HTTP %d: %s", action, resp.StatusCode, string(raw))
	}
	return raw, nil
}

// decodeAgentMessage extracts a message from the generated MessageResponse envelope.
func decodeAgentMessage(raw []byte, action string) (string, error) {
	var env agentMessageEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", fmt.Errorf("decode agent %s response: %w", action, err)
	}
	if !env.Success && env.Error != "" {
		return "", fmt.Errorf("agent %s: %s", action, env.Error)
	}
	return env.Message, nil
}
