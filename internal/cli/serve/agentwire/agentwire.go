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
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentmcp"
	"github.com/tysonthomas9/loomcli/internal/agentworktree"
	"github.com/tysonthomas9/loomcli/internal/gitrunner"
	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/opencode"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/prwatch"
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
	// APIBase is the loom serve origin the agent bridges call back to until
	// SetAPIBase gives the one serve actually bound; "" leaves presets with
	// bridge tools failing closed at launch.
	APIBase string
	// LoomBin runs `loom agent mcp-bridge`; "" is this executable.
	LoomBin string
	// GitHubRead is serve's host GitHub connector for the agents' github_read
	// tool; nil leaves github_read unoffered.
	GitHubRead agentsv1.GitHubReader
	// PRWatchHost is serve's host GitHub connector for the agents' PR
	// watches (OR10); nil leaves github/watch unavailable.
	PRWatchHost prwatch.Host
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
	apiBase  atomic.Pointer[string] // where the bridges call back

	mu       sync.Mutex // guards services and orders run against Stop
	services map[string]*loomagent.Service
	wg       sync.WaitGroup

	// ticker and now are the idle and retention timers' clock: time's own,
	// or a test's.
	ticker func(time.Duration) (<-chan time.Time, func())
	now    func() time.Time
}

// Start opens the registry and wires the OpenCode harness. Each workspace's
// service starts on its first request, or at once for a workspace that
// already has agents, so their pending messages and purges resume. OpenCode
// is reached on first use. The bridge and daemon tokens are signed with a
// key kept in the data dir; each agent with bridge tools gets its own token
// in its launch settings for `loom agent mcp-bridge` (design v2 §10.2).
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
	oc, err := newOpenCode(ctx, cfg, root)
	if err != nil {
		return nil, err
	}
	st, err := loomstore.Open(ctx, filepath.Join(cfg.Dir, "agents.db"))
	if err != nil {
		return nil, fmt.Errorf("agentwire: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	a := &API{store: st, tokens: tokens, opencode: oc, ctx: ctx, cancel: cancel, services: map[string]*loomagent.Service{}, ticker: realTicker, now: time.Now}
	a.SetAPIBase(cfg.APIBase)
	a.newSvc = func(ws string) (*loomagent.Service, func()) {
		var svc *loomagent.Service
		feed := sync.OnceFunc(func() { a.runLoop(svc.Feed("opencode")) })
		c := serviceConfig(st, ws, wt, cfg.Skills,
			map[string]loomharness.Harness{"opencode": lazyFeed{Harness: oc, start: feed}})
		c.RecoverFirst = true              // writes wait for the dispatcher's start-up Reconcile
		c.CatalogWarmUp = 45 * time.Second // MC1: OpenCode lists only some models ~20s after boot
		c.Bridge, c.Launch, c.Retire = bridge(a.APIBase, cfg.GitHubRead != nil), launch(a.APIBase, ws, tokens, cfg.GitHubRead != nil), retire(oc)
		svc = loomagent.New(c)
		return svc, feed
	}
	a.handler = agentsv1.New(a.service, nil).WithTokens(tokens).WithGitHub(cfg.GitHubRead).WithPRWatch(prWatcher(st, cfg.PRWatchHost))
	a.run(a.runIdle)
	known, _, err := st.ListAgents(ctx, loomstore.AgentFilter{IncludeArchived: true, IncludeDeleted: true})
	if err != nil {
		a.Stop()
		return nil, fmt.Errorf("agentwire: %w", err)
	}
	for _, ag := range known {
		a.service(ag.WorkspaceID)
	}
	if d, err := time.ParseDuration(os.Getenv(retentionEnv)); err == nil && d > 0 {
		loomstore.HistoryRetention, retentionTick = d, d/3
	}
	a.run(a.runRetention)
	return a, nil
}

// prWatcher is the agents' PR watches on st through host; nil without host.
func prWatcher(st *loomstore.Store, host prwatch.Host) agentsv1.PRWatcher {
	if host == nil {
		return nil
	}
	return prwatch.Service{Store: st, Host: host}
}

// newOpenCode returns the OpenCode adapter with Loom's presets and the
// `loom agent mcp-bridge` command registered under root.
func newOpenCode(ctx context.Context, cfg Config, root string) (*opencode.Adapter, error) {
	presets, err := loomagent.BuiltinPresets{}.List(ctx)
	if err != nil {
		return nil, err
	}
	if cfg.LoomBin == "" {
		if cfg.LoomBin, err = os.Executable(); err != nil {
			return nil, fmt.Errorf("agentwire: %w", err)
		}
	}
	oc := opencode.New(opencode.Config{Bin: cfg.OpenCodeBin, Env: cfg.OpenCodeEnv, Worktrees: root,
		Bridge: []string{cfg.LoomBin, "agent", "mcp-bridge"}})
	if err := oc.SetPresets(harnessPresets(presets)); err != nil {
		return nil, fmt.Errorf("agentwire: opencode presets: %w", err)
	}
	return oc, nil
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
	a.runLoop(svc.Dispatcher())
	if oc, _, err := a.store.ListAgents(a.ctx, loomstore.AgentFilter{WorkspaceID: ws, Harness: "opencode",
		IncludeArchived: true, Limit: 1}); err != nil || len(oc) > 0 {
		feed()
	}
	return svc
}

// run runs fn until Stop.
func (a *API) run(fn func(context.Context)) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctx.Err() != nil {
		return false
	}
	a.wg.Add(1)
	go func() { defer a.wg.Done(); fn(a.ctx) }()
	return true
}

// runLoop runs a service loop already registered with Drain (Dispatcher,
// Feed); after Stop it runs it with the done ctx, which only unregisters it.
func (a *API) runLoop(fn func(context.Context)) {
	if !a.run(fn) {
		fn(a.ctx)
	}
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

// SetAPIBase sets the loom serve origin the bridges of agents launched from
// now on call back to: the address serve actually bound, which differs from
// the configured one when that port was taken.
func (a *API) SetAPIBase(base string) { a.apiBase.Store(&base) }

// APIBase is the loom serve origin the bridges call back to.
func (a *API) APIBase() string { return *a.apiBase.Load() }

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
		ResolveRepo: resolveRepo, PrepareWorktree: prepareWorktree(skills, ws), Harnesses: harnesses}
}

// resolveRepo accepts a repo only as the absolute path of a git clone (a
// directory holding a .git that git can open, so not a dangling worktree
// link), so an unknown repo is a 400 at Create, not a git failure.
func resolveRepo(ctx context.Context, _ loomagent.Target, repo string) (string, error) {
	info, err := os.Stat(repo)
	if err == nil && info.IsDir() {
		_, err = os.Stat(filepath.Join(repo, ".git"))
	}
	if err == nil && info.IsDir() {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_, err = gitrunner.Exec{}.Run(ctx, repo, "rev-parse", "--git-dir")
	}
	if err != nil || !info.IsDir() || !filepath.IsAbs(repo) {
		return "", &loomagent.Error{Code: loomagent.CodePresetInvalid,
			Message: fmt.Sprintf("repo %q is not the absolute path of a repo clone", repo)}
	}
	return repo, nil
}

// bridge registers a preset's tools when the bridge serves them all and
// knows where serve is; otherwise the agent fails closed at launch. Without
// serve's host GitHub reader (github), github_read is not offered at all; it
// never falls back to gh or an agent credential.
func bridge(apiBase func() string, github bool) func(context.Context, loomagent.Preset) (loomagent.BridgeCaps, error) {
	return func(_ context.Context, p loomagent.Preset) (loomagent.BridgeCaps, error) {
		t := offered(p.Tools, github)
		if len(t) > 0 && apiBase() == "" {
			return loomagent.BridgeCaps{}, errors.New("no Agent API address for the bridge")
		}
		return loomagent.BridgeCaps{HasGitHubRead: slices.Contains(t, "github_read")}, agentmcp.Check(t)
	}
}

// offered is the preset tools the bridge serves: github_read only when serve
// has a host GitHub reader.
func offered(tools []string, github bool) []string {
	if github {
		return tools
	}
	return slices.DeleteFunc(slices.Clone(tools), func(t string) bool { return t == "github_read" })
}

// launch gives an agent whose preset has bridge tools its bridge settings,
// with the bridge token that names it, on every harness.
func launch(apiBase func() string, ws string, tokens *agentsv1.Tokens, github bool) func(context.Context, loomstore.Agent, string) (loomharness.Launch, error) {
	return func(ctx context.Context, a loomstore.Agent, harness string) (loomharness.Launch, error) {
		p, err := loomagent.BuiltinPresets{}.Get(ctx, a.Preset)
		t := offered(p.Tools, github)
		if err != nil || len(t) == 0 {
			return loomharness.Launch{}, err
		}
		return loomharness.Launch{Env: agentmcp.Config{API: apiBase(), Workspace: ws, Token: tokens.Agent(ws, a.AgentID),
			Repo: a.Repo, Harness: harness, Tools: t}.Env()}, nil
	}
}

// retire removes an archived or deleted OpenCode agent's bridge from
// OpenCode, and with it its token; Resume registers it again after an
// Unarchive. Other harnesses give the token to a process that ends with the
// turn, so nothing outlives it.
func retire(oc *opencode.Adapter) func(context.Context, loomstore.Agent) error {
	return func(ctx context.Context, a loomstore.Agent) error {
		if a.Harness != "opencode" || a.WorktreePath == nil {
			return nil
		}
		if err := oc.Unbridge(ctx, *a.WorktreePath); err != nil {
			return fmt.Errorf("agentwire: remove the agent's bridge: %w", err)
		}
		return nil
	}
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
