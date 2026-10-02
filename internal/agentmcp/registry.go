// Package agentmcp is `loom agent mcp-bridge`: one stdio MCP server that
// serves Loom's agent tools to every harness (design v2 §10.2). The bridge
// knows its agent only through the per-agent token it is launched with, and
// every tool calls the Agent API with that token. The server derives the
// caller from the token alone, so no tool argument can name or change who
// calls, and a lead reaches only its own children.
package agentmcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentsv1"
)

// The launch settings a harness gives the bridge (the agent's Launch.Env).
// Every harness gives them as the bridge process's environment; OpenCode's
// adapter registers them with the bridge command through OpenCode's runtime
// MCP API, never on disk. They hold the agent's token: never log or print
// them.
const (
	EnvAPI       = "LOOM_AGENT_API" // the loom serve origin
	EnvWorkspace = "LOOM_AGENT_WORKSPACE"
	EnvToken     = "LOOM_AGENT_TOKEN"   //nolint:gosec // G101: the name of the agent's bridge token variable, not a credential.
	EnvRepo      = "LOOM_AGENT_REPO"    // the agent's repo; its children are created there
	EnvHarness   = "LOOM_AGENT_HARNESS" // the agent's harness; its children run on it
	EnvTools     = "LOOM_AGENT_TOOLS"   // the tools to serve, comma-separated
)

// tools is the one registry of bridge tools, shared by every harness.
var tools = map[string]func(*mcp.Server, *bridge){
	"agent_create":  addAgentCreate,
	"agent_list":    addAgentList,
	"agent_get":     addAgentGet,
	"agent_send":    addAgentSend,
	"agent_archive": addAgentArchive,
}

// Check fails unless the bridge serves every tool in names.
func Check(names []string) error {
	for _, n := range names {
		if tools[n] == nil {
			return fmt.Errorf("the bridge does not serve %s", n)
		}
	}
	return nil
}

// Config is one agent's bridge settings.
type Config struct {
	API, Workspace, Token, Repo, Harness string
	Tools                                []string
}

// Env returns cfg as launch settings.
func (c Config) Env() map[string]string {
	return map[string]string{EnvAPI: c.API, EnvWorkspace: c.Workspace, EnvToken: c.Token, EnvRepo: c.Repo,
		EnvHarness: c.Harness, EnvTools: strings.Join(c.Tools, ",")}
}

// Load reads the settings from the environment. It fails closed when they
// name no token.
func Load() (Config, error) {
	if os.Getenv(EnvToken) == "" {
		return Config{}, fmt.Errorf("agentmcp: no %s: Loom launches the bridge with its agent's settings", EnvToken)
	}
	c := Config{API: os.Getenv(EnvAPI), Workspace: os.Getenv(EnvWorkspace), Token: os.Getenv(EnvToken),
		Repo: os.Getenv(EnvRepo), Harness: os.Getenv(EnvHarness)}
	if t := os.Getenv(EnvTools); t != "" {
		c.Tools = strings.Split(t, ",")
	}
	return c, nil
}

// API is the Agent API as the tools call it: the Agent API Go client
// (internal/loomagent/client), authenticated with cfg's token.
type API interface {
	Create(ctx context.Context, requestID string, b agentsv1.CreateBody) (agentsv1.Agent, error)
	List(ctx context.Context, f loomstore.AgentFilter) (agentsv1.AgentList, error)
	Get(ctx context.Context, agentID string) (agentsv1.Agent, error)
	Send(ctx context.Context, requestID, agentID, text string) (agentsv1.SendResult, error)
	Interrupt(ctx context.Context, requestID, agentID, text string) (agentsv1.SendResult, error)
	Archive(ctx context.Context, requestID, agentID, reason string) error
}

// Verify fails closed unless the Agent API accepts the bridge's token: a
// bridge for an agent that is archived or deleted, or with a forged token,
// serves nothing.
func Verify(ctx context.Context, api API) error {
	if _, err := api.List(ctx, loomstore.AgentFilter{Limit: 1}); err != nil {
		return fmt.Errorf("agentmcp: the Agent API refused this agent's bridge token: %w", err)
	}
	return nil
}

// bridge is what every tool calls through.
type bridge struct {
	api           API
	repo, harness string
}

// NewServer returns the MCP server with cfg's tools, calling api.
func NewServer(cfg Config, api API) (*mcp.Server, error) {
	if err := Check(cfg.Tools); err != nil {
		return nil, err
	}
	if len(cfg.Tools) > 0 && (cfg.API == "" || cfg.Workspace == "" || cfg.Token == "") {
		return nil, errors.New("agentmcp: tools need the Agent API address, workspace and token")
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "loom", Version: "1"},
		&mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}})
	b := &bridge{api: api, repo: cfg.Repo, harness: cfg.Harness}
	for _, n := range cfg.Tools {
		tools[n](s, b)
	}
	return s, nil
}
