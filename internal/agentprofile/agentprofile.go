// Package agentprofile resolves the on-disk layout of per-agent harness
// profiles: <projectDir>/.loom/agent-profiles/<agent>/{claude,codex}.
//
// It exists so the two halves of the feature cannot disagree about where a
// profile lives. The supervisor INJECTS these paths into the agent process as
// CLAUDE_CONFIG_DIR / CODEX_HOME at spawn; transcript mirroring and `loom
// doctor` DISCOVER them from other processes, where that injected environment
// is not present. A duplicated filepath.Join in each package is exactly how the
// injector and the reader drift apart, and a drift here is silent: the agent
// writes its transcript in one place and every reader looks in another.
//
// Directory existence is the whole contract — there is no config key and no
// flag — so the same layout works unchanged inside a container image, and an
// agent with no profile directory keeps inheriting the operator's ~/.claude and
// ~/.codex exactly as before.
//
// Deliberately leaf-level (stdlib only): it is imported by internal/sessions,
// which must not depend on internal/cli*, as well as by internal/cli packages.
package agentprofile

import (
	"os"
	"path/filepath"
)

// DirName is the directory under <projectDir>/.loom that holds per-agent
// profiles, one subdirectory per agent.
const DirName = "agent-profiles"

// Dir returns <projectDir>/.loom/agent-profiles/<agent>, or "" when either
// input is empty or when agent is not a single path segment.
//
// The segment check is not decorative: joining an empty agent would yield the
// agent-profiles root itself, so a stray "claude" directory there would be
// mistaken for a real profile, and a name carrying ".." or a separator would
// resolve a profile outside the workspace. Agent names come from daemon config
// today, but this resolver is also reachable from session metadata, so it
// validates rather than trusting its caller.
func Dir(projectDir, agent string) string {
	if projectDir == "" || agent == "" {
		return ""
	}
	if filepath.Base(agent) != agent || agent == "." || agent == ".." {
		return ""
	}
	return filepath.Join(projectDir, ".loom", DirName, agent)
}

// ClaudeConfigDir returns the agent's Claude profile directory when it exists,
// else "". This is the value the supervisor exports as CLAUDE_CONFIG_DIR.
func ClaudeConfigDir(projectDir, agent string) string {
	return backendDir(projectDir, agent, "claude")
}

// CodexHome returns the agent's Codex profile directory when it exists, else
// "". This is the value the supervisor exports as CODEX_HOME.
func CodexHome(projectDir, agent string) string {
	return backendDir(projectDir, agent, "codex")
}

func backendDir(projectDir, agent, backend string) string {
	root := Dir(projectDir, agent)
	if root == "" {
		return ""
	}
	dir := filepath.Join(root, backend)
	if !dirExists(dir) {
		return ""
	}
	return dir
}

// dirExists reports whether path is an existing directory. A regular file, a
// broken symlink, or an unreadable path all count as absent: discovery then
// degrades to the legacy roots instead of failing, because transcript
// mirroring and usage accounting are best-effort by contract.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
