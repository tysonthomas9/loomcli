// Package bridge runs `loom agent mcp-bridge`: it loads the agent's bridge
// settings, checks its token with the Agent API and serves its tools over
// stdio. It is apart from agentmcp because the Agent API client's tests
// import serve's wiring, which imports agentmcp.
package bridge

import (
	"context"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tysonthomas9/loomcli/internal/agentmcp"
	"github.com/tysonthomas9/loomcli/internal/loomagent/client"
)

// Run serves the bridge for the agent whose worktree is the working
// directory until ctx ends or the harness closes stdin. It fails closed
// when the agent has no settings or the Agent API refuses its token.
func Run(ctx context.Context) error {
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
	if err := agentmcp.Verify(ctx, api); err != nil {
		return err
	}
	return s.Run(ctx, &mcp.StdioTransport{})
}
