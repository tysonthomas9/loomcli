package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// bridge registers the agent's bridge (Bridge with its Launch.Env, and so
// its token) as the "loom" MCP server of location dir through OpenCode's
// runtime MCP API, then waits until OpenCode reports it connected for dir and
// its tool catalog has settled, so the agent's first turn has its tools. No
// settings are written to disk: the registration lives in the service's
// memory, per location (OpenCode b30c4d0 PUT /api/experimental/mcp/:server,
// server/src/handlers/mcp.ts; core/src/mcp/index.ts add). The PUT returns
// once the server connected or failed, and an unchanged config is a no-op
// (core/src/state.ts reconcile). The settle runs only after a fresh
// connect: not when the server was already connected with this config in a
// dir already settled, as on each later hand-off. A new or restarted
// OpenCode or an evicted location has lost the registration, so the settle
// runs again. A server that never connects fails closed: the agent is never
// prompted without its tools. An agent with no settings has no bridge.
func (c *Client) bridge(ctx context.Context, dir string, env map[string]string) error {
	if len(env) == 0 {
		return nil
	}
	if len(c.bridgeCmd) == 0 {
		return fmt.Errorf("opencode bridge: no bridge command configured: %w", loomharness.ErrUnavailable)
	}
	cfg := map[string]any{"type": "local", "command": c.bridgeCmd, "environment": env}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	connected, err := c.bridgeConnected(ctx, dir)
	if err != nil {
		return err
	}
	prev, ok := c.settled.Load(dir)
	fresh := !connected || !ok || prev != string(raw)
	if err := c.call(ctx, "PUT", "/api/experimental/mcp/loom?location[directory]="+url.QueryEscape(dir), map[string]any{"config": cfg}, nil); err != nil {
		c.settled.Delete(dir)
		return fmt.Errorf("opencode bridge for %s: %w", dir, err)
	}
	for deadline := time.Now().Add(agentWait); ; {
		ok, err := c.bridgeConnected(ctx, dir)
		if err != nil {
			return err
		}
		if ok {
			break
		}
		c.settled.Delete(dir)
		fresh = true
		if time.Now().After(deadline) {
			return fmt.Errorf("opencode bridge for %s not connected after %s: %w", dir, agentWait, loomharness.ErrUnavailable)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if fresh {
		if err := settle(ctx); err != nil {
			return err
		}
	}
	c.settled.Store(dir, string(raw))
	return nil
}

// bridgeConnected reports whether OpenCode lists the "loom" MCP server of
// location dir as connected.
func (c *Client) bridgeConnected(ctx context.Context, dir string) (bool, error) {
	var r struct {
		Data []mcpServer `json:"data"`
	}
	if err := c.call(ctx, "GET", "/api/mcp?location[directory]="+url.QueryEscape(dir), nil, &r); err != nil {
		return false, err
	}
	return slices.ContainsFunc(r.Data, func(m mcpServer) bool { return m.Name == "loom" && m.Status.Status == "connected" }), nil
}

// mcpServer is one entry of OpenCode's GET /api/mcp.
type mcpServer struct {
	Name   string `json:"name"`
	Status struct {
		Status string `json:"status"`
	} `json:"status"`
}

// unbridge removes the "loom" MCP server of location dir; one already gone
// (404) is removed.
func (c *Client) unbridge(ctx context.Context, dir string) error {
	c.settled.Delete(dir)
	err := c.call(ctx, "DELETE", "/api/experimental/mcp/loom?location[directory]="+url.QueryEscape(dir), nil, nil)
	if e := (*Error)(nil); errors.As(err, &e) && e.Status == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("opencode bridge for %s: %w", dir, err)
	}
	return nil
}

// catalogSettle is the bounded wait, after /api/mcp reports the loom server
// connected for a location, before that location's sessions list its tools.
// It covers OpenCode's 100 ms ToolsChanged debounce, and is interim until
// Loom's pinned OpenCode build makes a turn wait for its MCP servers. In
// OpenCode b30c4d0 (2.0.19): a location starts its MCP servers in the
// background (core/src/mcp/index.ts:533-546); its first tool discovery
// reads only servers already connected, so it never has a stdio server, and
// later tools reach the registry through ToolsChanged and a 100 ms debounced
// reload (core/src/tool/mcp.ts:37-141); a turn waits only for that first
// discovery before reading the registry (core/src/session/context.ts:127),
// and no API reads the registry.
var catalogSettle = 500 * time.Millisecond

// settle waits out catalogSettle, so a first turn sent now lists the tools.
func settle(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(catalogSettle):
		return nil
	}
}
