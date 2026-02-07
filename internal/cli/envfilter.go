package cli

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
	// Editor
	"EDITOR": true, "VISUAL": true,
}

// envAllowlistPrefixes contains prefixes for environment variable names
// that are allowed to be passed to spawned agent subprocesses.
var envAllowlistPrefixes = []string{
	"LOOM_",
	"BD_",
	"BEADS_",
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
