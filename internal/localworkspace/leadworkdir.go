package localworkspace

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
)

// EnvLeadWorkdir names the operator escape hatch that overrides the lead
// agent's working directory. The value must be an ABSOLUTE path; a relative
// value is ignored (it would resolve differently for the terminal launch and
// for the WebUI server, which is exactly the ambiguity this package exists to
// remove).
const EnvLeadWorkdir = "LOOM_LEAD_WORKDIR"

// LeadDirName is the workspace-root directory the lead agent runs in.
const LeadDirName = "lead"

// LeadWorkdir returns the lead agent's dedicated working directory for a
// workspace key, and whether one could be resolved at all.
//
// Lead is the one agent with no source_repo: it manages the backlog, builds
// nothing and runs no gate, so it gets a plain directory at the workspace root
// rather than a git worktree under worktrees/<repo>/. That directory is where
// the harness reads its ambient project instructions (AGENTS.md for codex,
// CLAUDE.md for claude) from, and where its cwd-keyed auto-memory lands.
//
// Resolution order:
//
//  1. LOOM_LEAD_WORKDIR, when set to an absolute path.
//  2. <workspacePath>/lead, when the workspace key resolves to a local path.
//
// An empty ("", false) result means the caller must fall back to its own default —
// os.Getwd for `loom lead`, the empty Cwd for a WebUI launch spec — which is
// the pre-existing behavior for an unresolvable workspace.
//
// wsKey is a workspace KEY, not a path: it is the same identifier
// RememberedAgentWorktree takes, so both launch paths can call this with what
// they already hold and provably compute the same string.
func LeadWorkdir(wsKey string) (string, bool) {
	if override := strings.TrimSpace(os.Getenv(EnvLeadWorkdir)); override != "" && filepath.IsAbs(override) {
		return filepath.Clean(override), true
	}
	wsKey = strings.TrimSpace(wsKey)
	if wsKey == "" {
		return "", false
	}
	cache, err := bootstrap.LoadStateCache()
	if err != nil || cache == nil {
		return "", false
	}
	wsPath := strings.TrimSpace(cache.Workspaces[wsKey].Path)
	if wsPath == "" {
		return "", false
	}
	return filepath.Join(wsPath, LeadDirName), true
}

// EnsureLeadWorkdir resolves the lead workdir and creates it if absent, so a
// caller can hand the path straight to a process as its working directory.
//
// Both launch paths need this, and for the same reason: the WebUI sets it as
// the PTY's Cwd (a missing directory fails the spawn outright) and `loom lead`
// runs the harness there. A creation failure degrades to ("", false) rather
// than to an error - lead always runs, it just runs in the caller's fallback.
func EnsureLeadWorkdir(wsKey string) (string, bool) {
	dir, ok := LeadWorkdir(wsKey)
	if !ok {
		return "", false
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("lead workdir: create failed, falling back", "dir", dir, "err", err)
		return "", false
	}
	return dir, true
}
