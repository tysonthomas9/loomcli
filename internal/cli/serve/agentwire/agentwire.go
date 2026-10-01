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

// Config says where the Agent API keeps its state and which workspace it serves.
type Config struct {
	WorkspaceID string      // the one workspace the Agent API serves
	Dir         string      // Loom's data dir (~/.loom): agents.db and worktrees/
	OpenCodeBin string      // the pinned OpenCode build
	OpenCodeEnv []string    // nil is the user's own environment (R1)
	Skills      store.Store // FleetDB skills staged into worktrees; nil stages none
}

// API is a running Agent API.
type API struct {
	handler  *agentsv1.Handler
	store    *loomstore.Store
	opencode *opencode.Adapter
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// Start opens the registry, wires the OpenCode harness and starts the
// service's feed and dispatcher. No bridge is wired yet (2.2a), so presets
// with bridge tools fail closed at launch.
func Start(ctx context.Context, cfg Config) (*API, error) {
	if cfg.WorkspaceID == "" || cfg.Dir == "" {
		return nil, errors.New("agentwire: a workspace and a data dir are required")
	}
	root := filepath.Join(cfg.Dir, "worktrees")
	if err := os.MkdirAll(root, 0o750); err != nil {
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
	svc := loomagent.New(loomagent.ServiceConfig{Store: st, WorkspaceID: cfg.WorkspaceID,
		Workspace: agentworktree.Port{W: wt}, PrepareWorktree: prepareWorktree(cfg.Skills, cfg.WorkspaceID),
		Harnesses: map[string]loomharness.Harness{"opencode": oc}})
	ctx, cancel := context.WithCancel(ctx)
	a := &API{store: st, opencode: oc, cancel: cancel,
		handler: agentsv1.New(func(ws string) *loomagent.Service {
			if ws == cfg.WorkspaceID {
				return svc
			}
			return nil
		}, nil)}
	for _, run := range []func(context.Context){svc.RunDispatcher,
		func(ctx context.Context) { svc.RunFeed(ctx, "opencode") }} {
		a.wg.Add(1)
		go func() { defer a.wg.Done(); run(ctx) }()
	}
	return a, nil
}

// Register mounts the Agent API routes; it is a webui AgentAPIRoutesFn.
func (a *API) Register(mux *http.ServeMux, workspace middleware.Middleware,
	validateToken func(token, workspace string) (string, error)) {
	a.handler.Register(mux, workspace, validateToken)
}

// Stop ends the feed and dispatcher, disconnects from OpenCode and closes the
// registry. The OpenCode service keeps running for the user's own clients.
func (a *API) Stop() {
	a.cancel()
	a.wg.Wait()
	a.opencode.Stop()
	_ = a.store.Close()
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
