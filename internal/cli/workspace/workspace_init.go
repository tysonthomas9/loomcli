package workspace

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// DefaultDaemonStartupTimeout is retained while workspace creation APIs still
// carry the old deferred-start field. FleetDB local mode does not start a
// per-workspace issue daemon.
const DefaultDaemonStartupTimeout = 120 * time.Second

// agentNamePool is the list of fun agent names to pick from when seeding loom.yaml.
var agentNamePool = []string{
	"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel",
	"indigo", "juliet", "kilo", "lima", "mike", "nova", "oscar", "papa",
	"quebec", "romeo", "sierra", "tango", "uniform", "victor", "whiskey", "xray",
	"yankee", "zulu", "atlas", "blaze", "cinder", "drift", "ember", "flare",
	"glyph", "helix", "ionic", "jade", "karma", "lumen", "nexus", "orbit",
	"prism", "quasar", "rune", "spark", "tidal", "umbra", "vortex", "warp",
}

// pickAgentNames randomly selects n unique names from agentNamePool.
func pickAgentNames(n int) []string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec // not cryptographic
	perm := rng.Perm(len(agentNamePool))
	names := make([]string, 0, n)
	for i := 0; i < n && i < len(perm); i++ {
		names = append(names, agentNamePool[perm[i]])
	}
	return names
}

// WriteLoomYaml writes a loom.yaml with default agent configuration into wsDir.
// The YAML is built via string formatting to avoid pulling in a YAML library.
// Returns the generated agent names so callers can create worktrees for them.
func WriteLoomYaml(wsDir string) ([]string, error) {
	names := pickAgentNames(2)

	var sb strings.Builder
	sb.WriteString("daemon:\n")
	sb.WriteString("  pid_file: .loom/daemon.pid\n")
	sb.WriteString("  log_dir: .loom/logs\n")
	sb.WriteString("  max_agents: 2\n")
	sb.WriteString("agents:\n")
	for _, name := range names {
		sb.WriteString(fmt.Sprintf("  - worktree: %s\n", name))
		sb.WriteString("    role: task\n")
		sb.WriteString("    auto: true\n")
	}

	yamlPath := filepath.Join(wsDir, "loom.yaml")
	if err := os.WriteFile(yamlPath, []byte(sb.String()), 0644); err != nil { //nolint:gosec // config file, not secret
		return nil, fmt.Errorf("failed to write loom.yaml: %w", err)
	}
	slog.Info("wrote default loom.yaml", "path", yamlPath, "agents", names)
	return names, nil
}

// CreateAgentWorktrees creates git worktrees for each agent in each repo.
// Worktrees are placed at <wsDir>/worktrees/<repoName>/<agentName>, matching
// the path convention in worktree_repo.go:resolveRepoWorktreeTarget.
// Best-effort: errors are logged but not fatal.
func CreateAgentWorktrees(wsDir string, repos []config.RepoConfig, agentNames []string) {
	for _, repo := range repos {
		for _, agent := range agentNames {
			wtPath := filepath.Join(wsDir, "worktrees", repo.Name, agent)
			if err := ensureRepoWorktree(repo.Path, wtPath, agent); err != nil {
				slog.Warn("failed to create agent worktree",
					"repo", repo.Name, "agent", agent, "err", err)
			} else {
				slog.Info("created agent worktree", "repo", repo.Name, "agent", agent)
			}
		}
	}
}

// EnsureDaemonForWorkspace is now a FleetDB-era readiness hook. There is no
// per-workspace issue daemon to start; callers keep using this function until
// the remaining workspace lifecycle names are cleaned up.
func EnsureDaemonForWorkspace(deps *cli.Deps, ctx context.Context, wsDir string, timeout time.Duration) error {
	_ = deps
	_ = timeout
	if ctx.Err() != nil {
		return fmt.Errorf("workspace readiness in %s cancelled: %w", wsDir, ctx.Err())
	}
	slog.Debug("workspace ready for FleetDB local mode", "path", wsDir)
	return nil
}

// StopDaemonForWorkspace is a no-op compatibility hook. FleetDB local mode
// does not run a per-workspace issue daemon.
func StopDaemonForWorkspace(deps *cli.Deps, wsDir string) {
	_ = deps
	if wsDir == "" {
		return
	}
	slog.Debug("workspace stop hook skipped for FleetDB local mode", "path", wsDir)
}

// ensureCurrentProjectRegistered registers the current working directory as a workspace
// in ~/.loom/config.yaml (if not already registered). This ensures
// the project that `loom serve` is running from always appears in the workspace
// list alongside any workspaces created via the UI.
//
// critical section inline; splitting would require passing cwd/wsName through a helper for no clarity gain.
//
//nolint:funlen // 57 lines: single-responsibility (register CWD) with a config-lock
func EnsureCurrentProjectRegistered() {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}

	// Guard: only auto-register directories that look like a loom project.
	if _, err := os.Stat(filepath.Join(cwd, ".git")); err != nil {
		slog.Debug("skipping CWD auto-registration: no .git directory", "path", cwd)
		return
	}

	wsName := filepath.Base(cwd)

	if err := config.WithConfigLock(func() error {
		cfg, err := config.LoadConfigUnlocked()
		if err != nil {
			return fmt.Errorf("config load failed: %w", err)
		}
		if cfg == nil {
			cfg = &config.LoomConfig{Workspaces: make(map[string]config.WorkspaceConfig)}
		}
		if cfg.Workspaces == nil {
			cfg.Workspaces = make(map[string]config.WorkspaceConfig)
		}

		// Check if already registered by path
		for _, ws := range cfg.Workspaces {
			if ws.Path == cwd {
				return nil
			}
		}
		if _, exists := cfg.Workspaces[wsName]; exists {
			return nil
		}

		repos := []config.RepoConfig{{Name: wsName, Path: cwd}}
		cfg.Workspaces[wsName] = config.WorkspaceConfig{ID: config.NewWorkspaceID(), Path: cwd, Repos: repos}
		if cfg.DefaultWorkspace == "" {
			cfg.DefaultWorkspace = wsName
		}
		found := false
		for _, n := range cfg.WorkspaceOrder {
			if n == wsName {
				found = true
				break
			}
		}
		if !found {
			cfg.WorkspaceOrder = append([]string{wsName}, cfg.WorkspaceOrder...)
		}

		return config.SaveConfigUnlocked(cfg)
	}); err != nil {
		slog.Warn("failed to register current project as workspace", "err", err)
	}
}

// EnsureDaemonsForAllWorkspaces marks ready configured workspaces as available
// to FleetDB-backed subscribers. There is no per-workspace issue daemon to
// start in local FleetDB mode. Best-effort: errors are logged, not fatal.
//
// When onReady is non-nil, it is called with the workspace UUID after each
// ready workspace is observed so subscribers can activate after config recovery.
func EnsureDaemonsForAllWorkspaces(deps *cli.Deps, ctx context.Context, onReady func(wsID string)) {
	_ = deps
	// Mark any interrupted workspaces as error before attempting daemon startup.
	RecoverIncompleteWorkspaces()

	cfg, err := config.LoadConfig()
	if err != nil || cfg == nil {
		return
	}

	for _, ws := range cfg.Workspaces {
		if ctx.Err() != nil {
			return
		}
		// Skip workspaces that aren't ready (error, creating, etc.)
		if ws.State != "" && ws.State != config.WorkspaceStateReady {
			continue
		}
		if onReady != nil && ws.ID != "" {
			onReady(ws.ID)
		}
	}
}
