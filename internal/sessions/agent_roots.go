package sessions

import (
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
)

// The resolvers in this file answer "where does THIS AGENT's harness keep its
// transcripts", as opposed to the process-scoped ClaudeConfigDir() and
// CodexSessionsRoot(), which answer it for whoever happens to be running.
//
// The distinction is a process boundary, and it is the whole reason these
// exist. The supervisor injects CLAUDE_CONFIG_DIR / CODEX_HOME into the AGENT
// process only, so the agent's own usage reader resolves the right roots from
// its environment. But the transcript is mirrored, and `loom doctor` runs,
// somewhere else entirely — the daemon and the operator's CLI — where the
// ambient environment is either unset or belongs to a different identity.
// Resolving from the environment there reads the wrong roots, which does not
// surface as an error: the transcript comes back empty and usage records zero.
//
// Precedence is therefore: the agent's profile directory, then the environment
// override, then the legacy $HOME path. Inside the agent process the first two
// agree by construction (the injected env var IS the profile dir), so the
// ordering only bites in the daemon and the CLI, where preferring the profile
// is the only choice that reads the right agent's transcripts. The env var
// keeps its full meaning for every agent WITHOUT a profile — the container and
// smoke-test paths depend on that.
//
// With no profile directory on disk, every resolver here is byte-identical to
// its process-scoped counterpart. That is the opt-in contract, not an accident.

// ClaudeConfigDirFor resolves Claude Code's config dir for a specific agent:
// the agent's profile dir when it exists, else CLAUDE_CONFIG_DIR, else
// ~/.claude. An empty projectDir or agent skips the profile lookup entirely.
func ClaudeConfigDirFor(projectDir, agent string) string {
	if dir := agentprofile.ClaudeConfigDir(projectDir, agent); dir != "" {
		return dir
	}
	return ClaudeConfigDir()
}

// ClaudeProjectsRootFor returns <ClaudeConfigDirFor>/projects, or "" when the
// config dir cannot be resolved. Claude Code names one subdirectory per
// encoded working directory underneath it.
func ClaudeProjectsRootFor(projectDir, agent string) string {
	root := ClaudeConfigDirFor(projectDir, agent)
	if root == "" {
		return ""
	}
	return filepath.Join(root, "projects")
}

// CodexSessionsRootFor resolves <codex home>/sessions for a specific agent:
// the agent's profile dir when it exists, else CODEX_HOME, else ~/.codex.
// Returns "" when the sessions dir does not exist, matching the stat guard
// CodexSessionsRoot applies — callers treat "" as "nothing to mirror".
func CodexSessionsRootFor(projectDir, agent string) string {
	home := agentprofile.CodexHome(projectDir, agent)
	if home == "" {
		return CodexSessionsRoot()
	}
	root := filepath.Join(home, "sessions")
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return root
	}
	return ""
}

// RootDir returns the workspace/project root the sessions directory hangs off
// (<root>/sessions → <root>). It is the projectDir half of the profile lookup,
// and it matches what the supervisor uses as its own ProjectDir and what
// cli.GetWorkspaceRuntimeDir() returns. Empty for a nil store, so a caller
// without a store falls back to the legacy roots rather than joining "".
func (s *Store) RootDir() string {
	if s == nil || s.dir == "" {
		return ""
	}
	return filepath.Dir(s.dir)
}
