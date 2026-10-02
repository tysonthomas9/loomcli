package agent

import (
	"context"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/agentmcp"
	"github.com/tysonthomas9/loomcli/internal/loomagent/client"
)

var mcpBridgeCmd = &cobra.Command{
	Use:   "mcp-bridge",
	Short: "Serve Loom's agent tools to a harness over stdio MCP",
	Long: `Serve Loom's agent tools to a harness over stdio MCP (design v2 §10.2).

A harness starts this for a Loom agent; it is not run by hand. The agent's
settings come from LOOM_AGENT_API, LOOM_AGENT_WORKSPACE, LOOM_AGENT_TOKEN,
LOOM_AGENT_REPO, LOOM_AGENT_HARNESS and LOOM_AGENT_TOOLS, or, for OpenCode,
from the settings file Loom keeps in the private git dir of the agent's
worktree (the working directory). Every tool acts as the agent the token
names. With no settings, or a token the Agent API refuses, it exits with an
error and serves nothing.`,
	Args:         cobra.NoArgs,
	SilenceUsage: true, // a refused start prints its reason, not the usage
	// stdout is the MCP channel: skip the root setup, which can log or reach
	// a backend.
	PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		cfg, err := agentmcp.Load(dir)
		if err != nil {
			return err
		}
		api := client.New(client.Config{BaseURL: cfg.API, Workspace: cfg.Workspace,
			Token: func(context.Context) (string, error) { return cfg.Token, nil }})
		s, err := agentmcp.NewServer(cfg, api)
		if err != nil {
			return err
		}
		if err := agentmcp.Verify(cmd.Context(), api); err != nil {
			return err
		}
		return s.Run(cmd.Context(), &mcp.StdioTransport{})
	},
}

func init() { agentCmd.AddCommand(mcpBridgeCmd) }
