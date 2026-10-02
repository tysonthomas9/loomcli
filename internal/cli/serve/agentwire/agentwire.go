// Package agentwire assembles the Agent API inside `loom serve` (design v2
// §3.2, §9.1): the agent registry, plain worktrees, the OpenCode adapter and
// the loomagent service, served by the agentsv1 routes. It is the one place
// serve builds them, so nothing else in serve reaches loomagent directly.
package agentwire

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/agentworktree"
	"github.com/tysonthomas9/loomcli/internal/gitrunner"
	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/opencode"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/skillmat"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentsv1"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// Config says where the Agent API keeps its state.
type Config struct {
	Dir         string      // Loom's data dir (~/.loom): agents.db and worktrees/
	OpenCodeBin string      // the pinned OpenCode build
	OpenCodeEnv []string    // nil is the user's own environment (R1)
	Skills      store.Store // FleetDB skills staged into worktrees; nil stages none
}

// API is a running Agent API: one service per workspace on a shared
// registry, worktree root and OpenCode adapter.
type API struct {
	handler  *agentsv1.Handler
	tokens   *agentsv1.Tokens
	store    *loomstore.Store
	opencode *opencode.Adapter
	ctx      context.Context
	cancel   context.CancelFunc
	newSvc   func(ws string) (*loomagent.Service, func())

	mu       sync.Mutex // guards services and orders run against Stop
	services map[string]*loomagent.Service
	wg       sync.WaitGroup
}

// Start opens the registry and wires the OpenCode harness. Each workspace's
// service starts on its first request, or at once for a workspace that
// already has agents, so their pending messages and purges resume. OpenCode
// is reached on first use. The bridge and daemon tokens are signed with a
// key kept in the data dir; no bridge is launched yet (2.2b), so presets with
// bridge tools fail closed at launch.
func Start(ctx context.Context, cfg Config) (*API, error) {
	if cfg.Dir == "" {
		return nil, errors.New("agentwire: a data dir is required")
	}
	root := filepath.Join(cfg.Dir, "worktrees")
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("agentwire: %w", err)
	}
	tokens, err := agentsv1.LoadTokens(filepath.Join(cfg.Dir, "agent-token.key"))
	if err != nil {
		return nil, fmt.Errorf("agentwire: %w", err)
	}
	wt, err := agentworktree.New(root, agentworktree.TargetLocal, gitrunner.Exec{})
	if err != nil {
		return nil, err
	}
	presets, err := loomagent.BuiltinPresets{}.List(ctx)
	if err != nil {
		return nil, err
	}
	oc := opencode.New(opencode.Config{Bin: cfg.OpenCodeBin, Env: cfg.OpenCodeEnv, Worktrees: root})
	if err := oc.SetPresets(harnessPresets(presets)); err != nil {
		return nil, fmt.Errorf("agentwire: opencode presets: %w", err)
	}
	st, err := loomstore.Open(ctx, filepath.Join(cfg.Dir, "agents.db"))
	if err != nil {
		return nil, fmt.Errorf("agentwire: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	a := &API{store: st, tokens: tokens, opencode: oc, ctx: ctx, cancel: cancel, services: map[string]*loomagent.Service{}}
	a.newSvc = func(ws string) (*loomagent.Service, func()) {
		var svc *loomagent.Service
		feed := sync.OnceFunc(func() { a.run(func(ctx context.Context) { svc.RunFeed(ctx, "opencode") }) })
		svc = loomagent.New(serviceConfig(st, ws, wt, cfg.Skills,
			map[string]loomharness.Harness{"opencode": lazyFeed{Harness: oc, start: feed}}))
		return svc, feed
	}
	a.handler = agentsv1.New(a.service, nil).WithTokens(tokens)
	known, _, err := st.ListAgents(ctx, loomstore.AgentFilter{IncludeArchived: true, IncludeDeleted: true})
	if err != nil {
		a.Stop()
		return nil, fmt.Errorf("agentwire: %w", err)
	}
	for _, ag := range known {
		a.service(ag.WorkspaceID)
	}
	return a, nil
}

// service returns ws's service, starting it with its dispatcher on first
// use. The workspace middleware has already checked that ws exists. Its
// OpenCode feed starts now only when ws has live (not deleted) OpenCode
// agents, else on the first Open (design v2 §8.1.4).
func (a *API) service(ws string) *loomagent.Service {
	a.mu.Lock()
	svc, ok := a.services[ws]
	if ok || a.ctx.Err() != nil {
		a.mu.Unlock()
		return svc
	}
	svc, feed := a.newSvc(ws)
	a.services[ws] = svc
	a.mu.Unlock()
	a.run(svc.RunDispatcher)
	if oc, _, err := a.store.ListAgents(a.ctx, loomstore.AgentFilter{WorkspaceID: ws, Harness: "opencode",
		IncludeArchived: true, Limit: 1}); err != nil || len(oc) > 0 {
		feed()
	}
	return svc
}

// run runs fn until Stop.
func (a *API) run(fn func(context.Context)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctx.Err() != nil {
		return
	}
	a.wg.Add(1)
	go func() { defer a.wg.Done(); fn(a.ctx) }()
}

// lazyFeed starts the harness feed before the first session Open, so a
// serve with no agents never contacts the harness.
type lazyFeed struct {
	loomharness.Harness
	start func()
}

func (h lazyFeed) Open(ctx context.Context, spec loomharness.OpenSpec) (loomharness.NativeRef, error) {
	h.start()
	return h.Harness.Open(ctx, spec)
}

// Tokens issues the per-agent bridge and daemon tokens the routes accept.
func (a *API) Tokens() *agentsv1.Tokens { return a.tokens }

// Register mounts the Agent API routes; it is a webui AgentAPIRoutesFn.
func (a *API) Register(mux *http.ServeMux, workspace middleware.Middleware,
	validateToken func(token, workspace string) (string, error)) {
	a.handler.Register(mux, workspace, validateToken)
}

// Stop ends the feed and dispatcher, disconnects from OpenCode and closes the
// registry. The OpenCode service keeps running for the user's own clients.
func (a *API) Stop() {
	a.mu.Lock()
	a.cancel()
	a.mu.Unlock()
	a.wg.Wait()
	a.opencode.Stop()
	_ = a.store.Close()
}

// serviceConfig wires ws's service on harnesses. Interrupt is left to its
// default, the current session's own Interrupt, so Send(interrupt), Archive
// cancelled and a harness switch all stop a running turn the same way.
func serviceConfig(st *loomstore.Store, ws string, wt *agentworktree.Worktrees, skills store.Store,
	harnesses map[string]loomharness.Harness) loomagent.ServiceConfig {
	return loomagent.ServiceConfig{Store: st, WorkspaceID: ws, Workspace: agentworktree.Port{W: wt},
		PrepareWorktree: prepareWorktree(skills, ws), Harnesses: harnesses}
}

// harnessPresets renders presets as the harness preset files.
func harnessPresets(presets []loomagent.Preset) []loomharness.PresetConfig {
	out := make([]loomharness.PresetConfig, len(presets))
	for i, p := range presets {
		out[i] = loomharness.PresetConfig{Name: p.Name, Persona: p.Persona, Tools: p.Tools}
	}
	return out
}

// prepareWorktree stages the workspace's and the preset's role skills into
// an agent's worktree before each hand-over, the same files for every
// harness. An unreachable skill store keeps the skills already staged.
func prepareWorktree(skills store.Store, workspace string) loomagent.PrepareWorktree {
	return func(ctx context.Context, _ loomagent.Target, a loomstore.Agent) error {
		if skills == nil || a.WorktreePath == nil {
			return nil
		}
		err := skillmat.MaterializeLeased(ctx, skills, workspace, a.Preset, *a.WorktreePath)
		if skillmat.IsStoreUnavailable(err) {
			slog.Warn("agentwire: skill store unavailable; keeping staged skills", "agent", a.AgentID, "error", err)
			return nil
		}
		return err
	}
}
