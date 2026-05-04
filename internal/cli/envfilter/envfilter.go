package envfilter

import (
	"os"
	"strings"
)

// envAllowlistExact contains environment variable names that are allowed
// to be passed to spawned agent subprocesses (exact match).
var envAllowlistExact = map[string]bool{
	// System
	"PATH": true, "HOME": true, "PWD": true, "TERM": true, "USER": true,
	"SHELL": true, "LOGNAME": true, "TMPDIR": true, "TZ": true,
	"COLUMNS": true, "LINES": true,
	// Locale
	"LANG": true, "LC_ALL": true, "LC_CTYPE": true, "LC_MESSAGES": true,
	// XDG
	"XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_RUNTIME_DIR": true,
	// Git/SSH
	"SSH_AUTH_SOCK": true, "GIT_SSH_COMMAND": true, "GIT_TERMINAL_PROMPT": true,
	"GIT_AUTHOR_NAME": true, "GIT_AUTHOR_EMAIL": true,
	"GIT_COMMITTER_NAME": true, "GIT_COMMITTER_EMAIL": true,
	// Network proxy
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
	"http_proxy": true, "https_proxy": true, "no_proxy": true,
	// Color
	"NO_COLOR": true, "FORCE_COLOR": true, "CLICOLOR": true, "CLICOLOR_FORCE": true,
	// AI backend keys
	"ANTHROPIC_API_KEY": true, "OPENAI_API_KEY": true,
	"GEMINI_API_KEY": true, "GOOGLE_API_KEY": true, "CURSOR_API_KEY": true,
	"CODEX_HOME": true,
	// Git hosting tokens (needed by container agents for git push)
	"GITHUB_TOKEN": true,
	// Editor
	"EDITOR": true, "VISUAL": true,
}

// envBlocklistExact contains environment variable names that are NEVER
// passed to subprocesses, even if they match the allowlist. Defense-in-depth
// against git-redirection and code-execution attacks.
var envBlocklistExact = map[string]bool{
	// Git redirection — can make git operate on wrong repo/worktree
	"GIT_DIR":                          true,
	"GIT_WORK_TREE":                    true,
	"GIT_INDEX_FILE":                   true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_CEILING_DIRECTORIES":          true,
	"GIT_COMMON_DIR":                   true,
	// Code execution — can run arbitrary commands via git
	"GIT_EXEC_PATH":    true,
	"GIT_TEMPLATE_DIR": true,
	"GIT_ASKPASS":      true,
	// Hook redirection — can run hooks from attacker-controlled directory
	"GIT_HOOKS_PATH": true,
	// Config injection — can set arbitrary git config including hooks
	"GIT_CONFIG":        true,
	"GIT_CONFIG_GLOBAL": true,
	"GIT_CONFIG_SYSTEM": true,
	"GIT_CONFIG_COUNT":  true, // env-based config injection (Git 2.31+)
}

// envBlocklistPrefixes contains prefixes that are NEVER passed to
// subprocesses. Needed for indexed env vars like GIT_CONFIG_KEY_0,
// GIT_CONFIG_VALUE_0 (Git 2.31+ env-based config injection).
var envBlocklistPrefixes = []string{
	"GIT_CONFIG_KEY_",
	"GIT_CONFIG_VALUE_",
}

// envAllowlistPrefixes contains prefixes for environment variable names
// that are allowed to be passed to spawned agent subprocesses.
var envAllowlistPrefixes = []string{
	"LOOM_",
}

// FilteredEnv returns os.Environ() filtered through the allowlist.
// Use this instead of os.Environ() when setting up subprocess environments.
func FilteredEnv() []string {
	return FilterEnv(os.Environ())
}

// FilterEnv filters the given environment variable slice through the allowlist.
// Only variables whose names match an exact allowlist entry or an allowed prefix
// are included. Malformed entries (missing '=') are excluded.
func FilterEnv(env []string) []string {
	result := make([]string, 0, len(env))
	for _, entry := range env {
		idx := strings.IndexByte(entry, '=')
		if idx < 0 {
			continue
		}
		name := entry[:idx]
		// Blocklist takes precedence over allowlist (defense-in-depth)
		if envBlocklistExact[name] {
			continue
		}
		blocked := false
		for _, prefix := range envBlocklistPrefixes {
			if strings.HasPrefix(name, prefix) {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		if envAllowlistExact[name] {
			result = append(result, entry)
			continue
		}
		for _, prefix := range envAllowlistPrefixes {
			if strings.HasPrefix(name, prefix) {
				result = append(result, entry)
				break
			}
		}
	}
	return result
}
