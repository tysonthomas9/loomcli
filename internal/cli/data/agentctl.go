package data

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/spf13/cobra"
)

// agentMessageEnvelope matches dto.MessageResponse returned by the agent
// control endpoints.
type agentMessageEnvelope struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// agentStopForce holds the --force flag for `loom data agent stop`.
var agentStopForce bool

// agentCmd is the `loom data agent` parent command (manage individual
// agents by name).
var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Control a single agent on the loom server (stop/start/restart/yield)",
}

var agentStopCmd = &cobra.Command{
	Use:   "stop <agent-name>",
	Short: "Yield the agent (or force-stop with --force)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgentControl(cmd.Context(), args[0], "stop", agentStopForce, !agentStopForce)
	},
}

var agentStartCmd = &cobra.Command{
	Use:   "start <agent-name>",
	Short: "Start a stopped agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgentControl(cmd.Context(), args[0], "start", false, false)
	},
}

var agentRestartCmd = &cobra.Command{
	Use:   "restart <agent-name>",
	Short: "Restart an agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgentControl(cmd.Context(), args[0], "restart", false, false)
	},
}

var agentYieldCmd = &cobra.Command{
	Use:   "yield <agent-name>",
	Short: "Request that the agent yield after its current task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgentControl(cmd.Context(), args[0], "yield", false, true)
	},
}

func init() {
	agentStopCmd.Flags().BoolVar(&agentStopForce, "force", false, "Force-stop immediately instead of yielding")
	agentCmd.AddCommand(agentStopCmd, agentStartCmd, agentRestartCmd, agentYieldCmd)
}

// runAgentControl POSTs to /api/workspaces/{ws}/agents/{name}/{action}.
// Setting force=true sends {"force": true} in the body (used by `stop`).
// expectAccepted indicates the server typically returns 202 on success
// (used by yield and non-force stop).
func runAgentControl(ctx context.Context, name, action string, force, expectAccepted bool) error {
	cli, baseURL, err := getHTTPClient()
	if err != nil {
		return err
	}
	wsID, err := resolveWorkspaceID(ctx, cli, baseURL)
	if err != nil {
		return err
	}

	path := baseURL + "/api/workspaces/" + url.PathEscape(wsID) + "/agents/" + url.PathEscape(name) + "/" + url.PathEscape(action)
	raw, err := postAgentAction(ctx, cli, path, action, force)
	if err != nil {
		return err
	}

	msg, err := decodeAgentMessage(raw, action)
	if err != nil {
		return err
	}
	if msg == "" {
		if expectAccepted {
			msg = fmt.Sprintf("agent %q %s requested", name, action)
		} else {
			msg = fmt.Sprintf("agent %q %s", name, action)
		}
	}
	return printMessageResult(os.Stdout, msg, outputFormat)
}

// postAgentAction sends the control POST and returns the raw response body,
// mapping HTTP status codes to error values. 200 and 202 are both treated
// as success (see internal/webui/handlers/agentcontrol).
func postAgentAction(ctx context.Context, cli *http.Client, path, action string, force bool) ([]byte, error) {
	var body io.Reader
	if force {
		b, _ := json.Marshal(map[string]bool{"force": true})
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if force {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusServiceUnavailable {
		return nil, fmt.Errorf("daemon unavailable on server")
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("agent %s: HTTP %d: %s", action, resp.StatusCode, string(raw))
	}
	return raw, nil
}

// decodeAgentMessage extracts a message from the dto.MessageResponse envelope.
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
