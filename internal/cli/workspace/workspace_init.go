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

// DefaultDaemonStartupTimeout is the fallback timeout for waiting for the daemon to become ready.
const DefaultDaemonStartupTimeout = 30 * time.Second

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

// ensureDaemonForWorkspace starts the bd daemon in the given workspace directory.
// It shells out to `bd daemon start` with Dir set to wsDir, then polls for readiness.
// If wsDir is not itself a git repository (e.g., a multi-repo workspace), the daemon
// is started with --local to avoid requiring a git repo at the workspace root.
// The function respects the provided context for cancellation and uses timeout as
// a fallback deadline for polling.
func EnsureDaemonForWorkspace(deps *cli.Deps, ctx context.Context, wsDir string, timeout time.Duration) error {
	if ctx.Err() != nil {
		return fmt.Errorf("daemon startup in %s cancelled: %w", wsDir, ctx.Err())
	}

	// Check if workspace dir is a git repo; if not, use --local mode
	args := []string{"daemon", "start"}
	if _, err := os.Stat(filepath.Join(wsDir, ".git")); err != nil {
		args = append(args, "--local")
	}
	result := deps.Exec.Run(wsDir, "bd", args...)
	if result.Err != nil {
		// bd daemon start returns non-zero when already running — proceed to poll anyway
		slog.Warn("bd daemon start returned error, will poll for readiness",
			"path", wsDir, "err", result.Err, "stderr", result.Stderr)
	}

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	deadlineTimer := time.NewTimer(timeout)
	defer deadlineTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("daemon startup in %s cancelled: %w", wsDir, ctx.Err())
		case <-deadlineTimer.C:
			return fmt.Errorf("daemon in %s did not become ready within %s", wsDir, timeout)
		case <-ticker.C:
			check := deps.Exec.Run(wsDir, "bd", "daemon", "status", "--json")
			if check.Err == nil && strings.Contains(check.Stdout, `"status":"running"`) {
				slog.Info("bd daemon started for workspace", "path", wsDir)
				return nil
			}
		}
	}
}

// stopDaemonForWorkspace is the cleanup counterpart to ensureDaemonForWorkspace.
// It runs `bd daemon stop` in wsDir on a best-effort basis: errors are logged at
// debug level but never returned, so callers can safely use it in cleanup paths
// without aborting other cleanup steps.
func StopDaemonForWorkspace(deps *cli.Deps, wsDir string) {
	if wsDir == "" {
		return
	}
	result := deps.Exec.Run(wsDir, "bd", "daemon", "stop")
	if result.Err != nil {
		slog.Debug("stopDaemonForWorkspace: bd daemon stop returned error (expected if not running)",
			"path", wsDir, "err", result.Err)
	}
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

	// Guard: only auto-register directories that look like a loom project
	// (must have .git/ so we know it's a repo, and .beads/ so we know bd can serve it).
	if _, err := os.Stat(filepath.Join(cwd, ".git")); err != nil {
		slog.Debug("skipping CWD auto-registration: no .git directory", "path", cwd)
		return
	}
	if _, err := os.Stat(filepath.Join(cwd, ".beads")); err != nil {
		slog.Debug("skipping CWD auto-registration: no .beads directory", "path", cwd)
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

// EnsureDaemonsForAllWorkspaces starts bd daemons for all configured workspaces
// that have a .beads/ directory. Runs staggered (200ms between each) to avoid
// thundering-herd on system resources. Skips the CWD workspace (already started
// by EnsureIssueBackendRunning in serve.go). Best-effort: errors are logged, not fatal.
func EnsureDaemonsForAllWorkspaces(deps *cli.Deps, ctx context.Context) {
	cwd, _ := os.Getwd()

	cfg, err := config.LoadConfig()
	if err != nil || cfg == nil {
		return
	}

	timeout := cfg.Daemon.GetStartupTimeout(DefaultDaemonStartupTimeout)

	for name, ws := range cfg.Workspaces {
		if ctx.Err() != nil {
			return
		}
		// Skip the CWD workspace — its daemon is managed by the main serve loop.
		if ws.Path == cwd {
			continue
		}
		// Only start daemons for workspaces that have a .beads/ directory.
		if _, err := os.Stat(filepath.Join(ws.Path, ".beads")); err != nil {
			continue
		}

		// Stagger to avoid thundering-herd.
		time.Sleep(200 * time.Millisecond)

		slog.Info("auto-starting daemon for workspace", "workspace", name, "path", ws.Path)
		if err := EnsureDaemonForWorkspace(deps, ctx, ws.Path, timeout); err != nil {
			slog.Warn("failed to auto-start daemon for workspace",
				"workspace", name, "path", ws.Path, "err", err)
		}
	}
}
