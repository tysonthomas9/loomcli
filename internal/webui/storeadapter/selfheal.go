package storeadapter

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// ResolveOrHealWorkspacePath returns the machine-local root path for a
// workspace. Fast path: when state.json already records a non-empty path it is
// returned with NO filesystem, git, or store access (pure, side-effect free).
//
// Heal path (only when the recorded path is empty): it loads the workspace +
// repos from the store, probes the fleet-db-derived on-disk location, verifies
// the checkout's origin remote matches the fleet-db RemoteURL, and on a verified
// match writes the path(s) back via bootstrap.MutateWorkspaceLocalState and
// returns the workspace dir. It never clones; an absent or unverifiable checkout
// returns "" (degrade as before) after an actionable log.
func ResolveOrHealWorkspacePath(ctx context.Context, s store.Store, key string) string {
	if p := resolveWorkspacePath(key); p != "" {
		return p
	}
	if s == nil || key == "" {
		return ""
	}
	ws, err := s.Workspaces().Get(ctx, key)
	if err != nil || ws == nil {
		return ""
	}
	return healWorkspacePath(ctx, s, ws)
}

// ListWorkspacePathsOrHeal is the healing variant of ListWorkspacePaths used by
// the reconcile loops: the same key->path map, but empty entries trigger a
// one-shot re-bind attempt against the on-disk checkout before being reported
// empty. It reuses the already-loaded workspace records (no extra Get).
func ListWorkspacePathsOrHeal(ctx context.Context, s store.Store) (map[string]string, error) {
	if s == nil {
		return nil, nil
	}
	all, err := s.Workspaces().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("storeadapter: list workspaces: %w", err)
	}
	if len(all) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(all))
	for _, ws := range all {
		if ws == nil {
			continue
		}
		p := resolveWorkspacePath(ws.Key)
		if p == "" {
			p = healWorkspacePath(ctx, s, ws)
		}
		out[ws.Key] = p
	}
	return out, nil
}

// healWorkspacePath attempts to re-bind a workspace whose local path is missing
// from state.json to an existing on-disk checkout at the deterministic default
// location. Returns the workspace dir on a verified re-bind, else "".
//
// It NEVER clones — re-cloning a genuinely missing checkout stays an explicit
// user action. The only side effect on success is the state.json write.
func healWorkspacePath(ctx context.Context, s store.Store, ws *domain.Workspace) string {
	if ws == nil || ws.Key == "" || ws.Name == "" {
		return ""
	}
	// Mirror config.GetWorkspaceDir(name) without importing cli/config (which
	// would create an import cycle: cli imports webui). The default workspace
	// dir is keyed by the lowercase workspace Name, and LoomDir() honors
	// LOOM_CONFIG_DIR so this resolves correctly for both the desktop app and
	// the dev serve stack.
	wsDir := filepath.Join(bootstrap.LoomDir(), "workspaces", ws.Name)
	if info, err := os.Stat(wsDir); err != nil || !info.IsDir() {
		slog.Info("workspace self-heal: no checkout at expected location; leaving unbound (re-clone required)",
			"workspace", ws.Key, "expected_dir", wsDir)
		return ""
	}

	repos, err := s.Repos().List(ctx, ws.Key)
	if err != nil {
		return ""
	}

	verified := make(map[string]string, len(repos)) // repoName -> repoPath
	for _, r := range repos {
		if r == nil {
			continue
		}
		repoPath := filepath.Join(wsDir, r.Name) // <wsDir>/<repoName>
		if fi, statErr := os.Stat(repoPath); statErr != nil || !fi.IsDir() {
			continue
		}
		if !verifyCheckout(repoPath, r) {
			continue
		}
		verified[r.Name] = repoPath
	}

	if len(verified) == 0 {
		slog.Warn("workspace self-heal: directory exists but no repo checkout verified; leaving unbound",
			"workspace", ws.Key, "dir", wsDir, "repos", len(repos))
		return ""
	}

	if err := bindHealedPaths(ws.Key, wsDir, verified); err != nil {
		slog.Warn("workspace self-heal: re-bind write failed", "workspace", ws.Key, "err", err)
		return ""
	}
	slog.Info("workspace self-heal: re-bound local checkout",
		"workspace", ws.Key, "path", wsDir, "repos_bound", len(verified))
	return wsDir
}

// verifyCheckout reports whether the git checkout at repoPath is the one
// fleet-db expects for repo r. When fleet-db records a RemoteURL, the checkout's
// origin remote must match it; when fleet-db has no canonical URL (e.g. a
// locally-created repo with no shared remote), any real git checkout at the
// authoritative fleet-db-derived location is accepted.
func verifyCheckout(repoPath string, r *domain.Repo) bool {
	if strings.TrimSpace(r.RemoteURL) == "" {
		// fleet-db has no canonical URL (locally-created repo / no shared remote
		// yet). The location came from authoritative fleet-db data, so any real
		// git checkout there is good enough to re-bind.
		return isGitWorkTree(repoPath)
	}
	remoteName := r.Remote
	if remoteName == "" {
		remoteName = "origin"
	}
	got, err := localworkspace.GitRemoteURL(repoPath, remoteName)
	if err != nil {
		return false // not a git work tree / remote unset
	}
	return sameRemote(got, r.RemoteURL)
}

// isGitWorkTree reports whether dir has a .git entry (a directory for a normal
// clone, a file for a linked worktree).
func isGitWorkTree(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// sameRemote compares two clone URLs tolerantly: trim whitespace and a single
// trailing "/" and ".git". Intentionally strict beyond that (ssh vs https forms
// stay distinct) to avoid binding the wrong directory.
func sameRemote(a, b string) bool {
	norm := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.TrimSuffix(s, "/")
		return strings.TrimSuffix(s, ".git")
	}
	return norm(a) == norm(b)
}

// bindHealedPaths writes the re-derived workspace + repo paths back to the
// per-machine state cache via the exported, file-locked, LOOM_CONFIG_DIR-aware
// mutator, so concurrent heals (e.g. a terminal attach and a reconcile tick)
// serialize at the write and converge. Mirrors saveLocalWorkspaceState.
func bindHealedPaths(wsKey, wsDir string, repoPaths map[string]string) error {
	return bootstrap.MutateWorkspaceLocalState(wsKey, func(local *bootstrap.WorkspaceLocalState) error {
		if local.Path == "" {
			local.Path = wsDir // idempotent: leave any concurrently-written value intact
		}
		if local.Repos == nil {
			local.Repos = make(map[string]string, len(repoPaths))
		}
		for name, p := range repoPaths {
			local.Repos[name] = p
		}
		return nil
	})
}
