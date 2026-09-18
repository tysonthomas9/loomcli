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
	"strconv"
	"time"

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

// Flags for `loom data agent yield`. agentYieldTTL bounds the drain; a drain
// also lapses when the supervisor it was addressed to restarts, so
// --until-restart (ttl_seconds "0") relies on that supersession alone.
var (
	agentYieldTTL          time.Duration
	agentYieldUntilRestart bool
)

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
		var body map[string]any
		if agentStopForce {
			body = map[string]any{"force": true}
		}
		return runAgentControl(cmd.Context(), args[0], "stop", body, !agentStopForce)
	},
}

var agentStartCmd = &cobra.Command{
	Use:   "start <agent-name>",
	Short: "Start a stopped agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgentControl(cmd.Context(), args[0], "start", nil, false)
	},
}

var agentRestartCmd = &cobra.Command{
	Use:   "restart <agent-name>",
	Short: "Restart an agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAgentControl(cmd.Context(), args[0], "restart", nil, false)
	},
}

var agentYieldCmd = &cobra.Command{
	Use:   "yield <agent-name>",
	Short: "Request that the agent yield after its current task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if agentYieldUntilRestart && cmd.Flags().Changed("ttl") {
			return fmt.Errorf("--ttl and --until-restart are mutually exclusive")
		}
		ttl := agentYieldTTL
		if agentYieldUntilRestart {
			ttl = 0
		}
		// The TTL rides the generic {"payload": {...}} passthrough the yield
		// handler already merges into the queued command, so no server-side
		// request field is needed.
		// Round a sub-second TTL up to one second: "0" is reserved for
		// --until-restart, so truncating 500ms to it would silently turn a
		// short drain into an unbounded one.
		seconds := int(ttl.Seconds())
		if ttl > 0 && seconds == 0 {
			seconds = 1
		}
		body := map[string]any{"payload": map[string]any{
			"ttl_seconds": strconv.Itoa(seconds),
		}}
		return runAgentControl(cmd.Context(), args[0], "yield", body, true)
	},
}

func init() {
	agentStopCmd.Flags().BoolVar(&agentStopForce, "force", false, "Force-stop immediately instead of yielding")
	agentYieldCmd.Flags().DurationVar(&agentYieldTTL, "ttl", 2*time.Hour, "How long the drain stays in effect before lapsing")
	agentYieldCmd.Flags().BoolVar(&agentYieldUntilRestart, "until-restart", false, "Drain until the supervisor restarts, with no time limit")
	agentCmd.AddCommand(agentStopCmd, agentStartCmd, agentRestartCmd, agentYieldCmd)
}

// runAgentControl POSTs to /api/workspaces/{ws}/agents/{name}/{action}.
// A nil body sends no request body at all; a non-nil body is sent as JSON.
// expectAccepted indicates the server typically returns 202 on success
// (used by yield and non-force stop).
func runAgentControl(ctx context.Context, name, action string, body map[string]any, expectAccepted bool) error {
	cli, baseURL, err := getHTTPClient()
	if err != nil {
		return err
	}
	wsID, err := resolveWorkspaceID(ctx, cli, baseURL)
	if err != nil {
		return err
	}

	path := baseURL + "/api/workspaces/" + url.PathEscape(wsID) + "/agents/" + url.PathEscape(name) + "/" + url.PathEscape(action)
	raw, err := postAgentAction(ctx, cli, path, action, body)
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
func postAgentAction(ctx context.Context, cli *http.Client, path, action string, payload map[string]any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	// Content-Type is set if and only if a body is actually sent.
	if payload != nil {
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
