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
	mu       sync.Mutex // orders run against Stop
	wg       sync.WaitGroup
}

// Start opens the registry, wires the OpenCode harness and starts the
// service's dispatcher; the OpenCode feed starts on first use. No bridge is
// wired yet (2.2a), so presets with bridge tools fail closed at launch.
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
	ctx, cancel := context.WithCancel(ctx)
	a := &API{store: st, opencode: oc, cancel: cancel}
	var svc *loomagent.Service
	feed := sync.OnceFunc(func() { a.run(ctx, func(ctx context.Context) { svc.RunFeed(ctx, "opencode") }) })
	svc = loomagent.New(loomagent.ServiceConfig{Store: st, WorkspaceID: cfg.WorkspaceID,
		Workspace: agentworktree.Port{W: wt}, PrepareWorktree: prepareWorktree(cfg.Skills, cfg.WorkspaceID),
		Harnesses: map[string]loomharness.Harness{"opencode": lazyFeed{Harness: oc, start: feed}}})
	a.handler = agentsv1.New(func(ws string) *loomagent.Service {
		if ws == cfg.WorkspaceID {
			return svc
		}
		return nil
	}, nil)
	a.run(ctx, svc.RunDispatcher)
	// OpenCode is reached on first use (design v2 §8.1.4): the feed starts
	// now only when OpenCode agents are already recorded, else on the first Open.
	if known, _, err := st.ListAgents(ctx, loomstore.AgentFilter{Harness: "opencode", IncludeArchived: true, Limit: 1}); err != nil || len(known) > 0 {
		feed()
	}
	return a, nil
}

// run runs fn until Stop.
func (a *API) run(ctx context.Context, fn func(context.Context)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	a.wg.Add(1)
	go func() { defer a.wg.Done(); fn(ctx) }()
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
