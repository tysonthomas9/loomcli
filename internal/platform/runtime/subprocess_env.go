package runtime //nolint:revive // The approved target architecture names this platform mechanism runtime.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SubprocessEnvProfile names the trust placement of a subprocess. Profiles are
// intentionally closed: an unknown value filters every ambient variable.
type SubprocessEnvProfile uint8

const (
	// SubprocessEnvTrustedLocalCLI is for a local, user-owned agent CLI. It
	// admits the configured model-provider credentials and non-secret LOOM_
	// context, while control-plane, forge, and Git-redirection authority stays
	// denied.
	SubprocessEnvTrustedLocalCLI SubprocessEnvProfile = iota + 1
	// SubprocessEnvInteractionChild is the least-privilege ambient environment
	// for an Interaction-owned terminal child. Session authority is appended by
	// Interaction after this filter and is never inherited from the parent.
	SubprocessEnvInteractionChild
	// SubprocessEnvDriverRemote is the strict environment for workflow and
	// remote task-runner processes. It carries only process mechanics and the
	// exact Driver bootstrap controls needed before a run-scoped envelope is
	// appended.
	SubprocessEnvDriverRemote
	// SubprocessEnvDriverLocalTaskRunner is the trusted-local Driver profile. It
	// is the remote profile plus the exact model-provider credential set needed
	// by a local backend CLI; forge and control-plane credentials remain denied.
	SubprocessEnvDriverLocalTaskRunner
)

type subprocessEnvPolicy struct {
	allowExact    map[string]struct{}
	allowPrefixes []string
}

var trustedLocalCLIEnv = subprocessEnvPolicy{
	allowExact: map[string]struct{}{
		// Process and terminal mechanics.
		"PATH": {}, "HOME": {}, "PWD": {}, "TERM": {}, "USER": {},
		"SHELL": {}, "LOGNAME": {}, "TMPDIR": {}, "TZ": {},
		"COLUMNS": {}, "LINES": {},
		// Locale and XDG homes.
		"LANG": {}, "LC_ALL": {}, "LC_CTYPE": {}, "LC_MESSAGES": {},
		"XDG_CONFIG_HOME": {}, "XDG_DATA_HOME": {}, "XDG_RUNTIME_DIR": {},
		// Git identity and the user's local SSH agent. Git repository/config
		// redirection variables are denied globally below.
		"SSH_AUTH_SOCK": {}, "GIT_SSH_COMMAND": {}, "GIT_TERMINAL_PROMPT": {},
		"GIT_AUTHOR_NAME": {}, "GIT_AUTHOR_EMAIL": {},
		"GIT_COMMITTER_NAME": {}, "GIT_COMMITTER_EMAIL": {},
		// Proxy, color, and editor settings.
		"HTTP_PROXY": {}, "HTTPS_PROXY": {}, "NO_PROXY": {},
		"http_proxy": {}, "https_proxy": {}, "no_proxy": {},
		"NO_COLOR": {}, "FORCE_COLOR": {}, "CLICOLOR": {}, "CLICOLOR_FORCE": {},
		"EDITOR": {}, "VISUAL": {},
		// Exact local model-provider credentials. These are deliberately not a
		// prefix allowlist.
		"ANTHROPIC_API_KEY": {}, "OPENAI_API_KEY": {},
		"GEMINI_API_KEY": {}, "GOOGLE_API_KEY": {}, "CURSOR_API_KEY": {},
		"CODEX_HOME": {}, "CLAUDE_CODE_OAUTH_TOKEN": {},
		// Exact deterministic test controls; arbitrary STUB_* values stay out.
		"STUB_CODEX_EPIC_RUNNER": {}, "STUB_CODEX_INVOCATIONS": {},
	},
	allowPrefixes: []string{"LOOM_"},
}

var interactionChildEnv = subprocessEnvPolicy{
	allowExact: map[string]struct{}{
		"PATH": {}, "HOME": {}, "PWD": {}, "OLDPWD": {},
		"TMPDIR": {}, "TMP": {}, "TEMP": {}, "TERM": {},
		"USER": {}, "LOGNAME": {}, "SHELL": {}, "TZ": {}, "LANG": {},
		"NO_COLOR": {}, "FORCE_COLOR": {}, "CLICOLOR": {}, "CLICOLOR_FORCE": {},
		"HTTP_PROXY": {}, "HTTPS_PROXY": {}, "NO_PROXY": {},
		"http_proxy": {}, "https_proxy": {}, "no_proxy": {},
		"CODEX_HOME": {}, "CLAUDE_CONFIG_DIR": {}, "TMUX_TMPDIR": {},
		"GIT_AUTHOR_NAME": {}, "GIT_AUTHOR_EMAIL": {},
		"GIT_COMMITTER_NAME": {}, "GIT_COMMITTER_EMAIL": {},
	},
	allowPrefixes: []string{"LC_"},
}

var driverRemoteEnv = subprocessEnvPolicy{
	allowExact: map[string]struct{}{
		"PATH": {}, "HOME": {}, "PWD": {}, "OLDPWD": {},
		"TMPDIR": {}, "TMP": {}, "TEMP": {}, "TERM": {},
		"USER": {}, "LOGNAME": {}, "SHELL": {}, "TZ": {}, "LANG": {},
		"LOOM_CONFIG_DIR": {}, "LOOM_FLUE_AGENT_MODEL": {},
		// Exact host/runner bootstrap controls. Run identity and authority are
		// appended by Driver only after this ambient filter.
		"LOOM_HOST_BRIDGE_HELPER":          {},
		"LOOM_DRIVER_TASK_RUNNER_CMD_JSON": {},
		"LOOM_DRIVER_TASK_RUNNER_CMD":      {},
	},
	allowPrefixes: []string{"LC_"},
}

var driverLocalTaskRunnerEnv = subprocessEnvPolicy{
	allowExact: mergeSubprocessEnvExact(driverRemoteEnv.allowExact, map[string]struct{}{
		"ANTHROPIC_API_KEY":              {},
		"CLAUDE_CODE_OAUTH_TOKEN":        {},
		"OPENAI_API_KEY":                 {},
		"CODEX_API_KEY":                  {},
		"CODEX_HOME":                     {},
		"GEMINI_API_KEY":                 {},
		"GOOGLE_API_KEY":                 {},
		"GOOGLE_APPLICATION_CREDENTIALS": {},
		"CURSOR_API_KEY":                 {},
		// Backend executable overrides are trusted-local operator controls.
		// The bundled runner documents and consumes this closed set; admitting
		// them here keeps fail-closed binary selection intact across the Driver
		// subprocess boundary without widening remote workflow environments.
		"LOOM_CLAUDE_BIN":       {},
		"LOOM_CODEX_BIN":        {},
		"LOOM_CURSOR_BIN":       {},
		"LOOM_GEMINI_BIN":       {},
		"LOOM_LOCALDOGFOOD_BIN": {},
		"LOOM_OPENCODE_BIN":     {},
	}),
	allowPrefixes: driverRemoteEnv.allowPrefixes,
}

// hardBlockedSubprocessEnvExact contains authority and process-redirection
// inputs that no ambient trust profile may inherit. Run/session-scoped values
// with similar names are appended by their capability owner after filtering.
var hardBlockedSubprocessEnvExact = map[string]struct{}{
	// Git repository, object, hook, executable, and config redirection.
	"GIT_DIR": {}, "GIT_WORK_TREE": {}, "GIT_INDEX_FILE": {},
	"GIT_OBJECT_DIRECTORY": {}, "GIT_ALTERNATE_OBJECT_DIRECTORIES": {},
	"GIT_CEILING_DIRECTORIES": {}, "GIT_COMMON_DIR": {},
	"GIT_EXEC_PATH": {}, "GIT_TEMPLATE_DIR": {}, "GIT_ASKPASS": {},
	"GIT_HOOKS_PATH": {}, "GIT_CONFIG": {}, "GIT_CONFIG_GLOBAL": {},
	"GIT_CONFIG_SYSTEM": {}, "GIT_CONFIG_COUNT": {},
	// Forge and provider-host credentials/configuration are connector-owned.
	"GITHUB_TOKEN": {}, "GITHUB_TOKEN_FILE": {}, "GH_TOKEN": {},
	"GITLAB_TOKEN": {}, "DAYTONA_API_KEY": {},
	"DAYTONA_CREDENTIAL_FILE": {}, "DAYTONA_SDK_IMPORT": {},
	// FleetDB/control-plane bootstrap authority never flows ambiently.
	"LOOM_FLEET_API_KEY": {},
	// TaskRun request JSON contains the same scoped lease credential that is
	// also projected through its exact owner envelope. A model backend needs
	// the API tuple, not the broader request payload or its duplicate token.
	"LOOM_TASK_RUN_REQUEST_JSON": {},
}

var hardBlockedSubprocessEnvPrefixes = []string{
	"GIT_CONFIG_",
	"LOOM_FLEET_DB_",
	"LOOM_DRIVER_FLEET_DB_",
	"LOOM_FLEETDB_",
	"FLEET_",
}

var sensitiveSubprocessEnvPrefixes = []string{
	"AWS_", "AZURE_", "GCP_", "GOOGLE_",
}

var sensitiveSubprocessEnvFragments = []string{
	"SECRET", "TOKEN", "PASSWORD", "PRIVATE_KEY", "ACCESS_KEY", "API_KEY", "CREDENTIAL",
}

// CurrentSubprocessEnv filters the current process environment for profile.
func CurrentSubprocessEnv(profile SubprocessEnvProfile) []string {
	return FilterSubprocessEnv(profile, os.Environ())
}

// PinExecutableDirOnPath makes plain sibling-command lookups resolve beside
// the executable that launched the current process. Packaged Desktop keeps
// its loom sidecar there; pinning that directory prevents an older global
// install from speaking an incompatible local protocol. Invalid or relative
// executable paths leave the environment unchanged.
func PinExecutableDirOnPath(env []string, executable string) []string {
	executable = strings.TrimSpace(executable)
	if executable == "" || !filepath.IsAbs(executable) {
		return env
	}
	executableDir := filepath.Clean(filepath.Dir(executable))
	pathValue := ""
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if ok && name == "PATH" {
			pathValue = value
			continue
		}
		out = append(out, entry)
	}
	pathEntries := []string{executableDir}
	for _, entry := range filepath.SplitList(pathValue) {
		if filepath.Clean(entry) != executableDir {
			pathEntries = append(pathEntries, entry)
		}
	}
	return append(out, "PATH="+strings.Join(pathEntries, string(os.PathListSeparator)))
}

// PinExecutableDirForLoginShell preserves the executable pin across model
// backends that launch login shells. Codex intentionally restores a captured
// user shell environment for tool calls, which can replace the PATH inherited
// by the backend process. A run-scoped ZDOTDIR/BASH_ENV reapplies only the
// trusted sibling directory without reading or modifying user dotfiles.
//
// The returned cleanup must remain deferred until the backend child exits.
func PinExecutableDirForLoginShell(env []string, executable string) ([]string, func(), error) {
	executable = strings.TrimSpace(executable)
	if executable == "" || !filepath.IsAbs(executable) {
		return env, func() {}, nil
	}
	executableDir := filepath.Clean(filepath.Dir(executable))
	profileDir, err := os.MkdirTemp("", "loom-task-shell-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create task shell profile: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(profileDir) }
	profile := []byte("if [ -n \"${LOOM_PINNED_EXECUTABLE_DIR:-}\" ]; then\n" +
		"  export PATH=\"${LOOM_PINNED_EXECUTABLE_DIR}:${PATH}\"\n" +
		"fi\n")
	for _, name := range []string{".zshenv", ".zprofile"} {
		if err := os.WriteFile(filepath.Join(profileDir, name), profile, 0o600); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("write task zsh profile %s: %w", name, err)
		}
	}
	bashProfilePath := filepath.Join(profileDir, "shell-env")
	if err := os.WriteFile(bashProfilePath, profile, 0o600); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("write task bash profile: %w", err)
	}
	pinned := PinExecutableDirOnPath(env, executable)
	pinned = replaceSubprocessEnv(pinned, "ZDOTDIR", profileDir)
	pinned = replaceSubprocessEnv(pinned, "BASH_ENV", bashProfilePath)
	pinned = replaceSubprocessEnv(pinned, "LOOM_PINNED_EXECUTABLE_DIR", executableDir)
	return pinned, cleanup, nil
}

func replaceSubprocessEnv(env []string, name, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		entryName, _, ok := strings.Cut(entry, "=")
		if ok && entryName == name {
			continue
		}
		out = append(out, entry)
	}
	return append(out, name+"="+value)
}

// FilterSubprocessEnv returns only ambient variables admitted by profile.
// Malformed entries and unknown profiles fail closed. Input order and values
// are preserved so callers can append an owner-minted run/session envelope.
func FilterSubprocessEnv(profile SubprocessEnvProfile, env []string) []string {
	policy, ok := subprocessEnvironmentPolicy(profile)
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, found := strings.Cut(entry, "=")
		name = strings.TrimSpace(name)
		if !found || name == "" || !subprocessEnvironmentNameAllowed(policy, name) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func subprocessEnvironmentPolicy(profile SubprocessEnvProfile) (subprocessEnvPolicy, bool) {
	switch profile {
	case SubprocessEnvTrustedLocalCLI:
		return trustedLocalCLIEnv, true
	case SubprocessEnvInteractionChild:
		return interactionChildEnv, true
	case SubprocessEnvDriverRemote:
		return driverRemoteEnv, true
	case SubprocessEnvDriverLocalTaskRunner:
		return driverLocalTaskRunnerEnv, true
	default:
		return subprocessEnvPolicy{}, false
	}
}

func subprocessEnvironmentNameAllowed(policy subprocessEnvPolicy, name string) bool {
	upper := strings.ToUpper(name)
	if subprocessEnvironmentHardBlocked(upper) {
		return false
	}
	// Exact profile entries are the only way to admit a credential-bearing
	// variable. This is what distinguishes trusted-local model credentials
	// from a broad ambient secret fallback.
	if _, ok := policy.allowExact[name]; ok {
		return true
	}
	if subprocessEnvironmentSensitive(upper) {
		return false
	}
	for _, prefix := range policy.allowPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func subprocessEnvironmentHardBlocked(upper string) bool {
	if _, blocked := hardBlockedSubprocessEnvExact[upper]; blocked {
		return true
	}
	for _, prefix := range hardBlockedSubprocessEnvPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

func subprocessEnvironmentSensitive(upper string) bool {
	for _, prefix := range sensitiveSubprocessEnvPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	for _, fragment := range sensitiveSubprocessEnvFragments {
		if strings.Contains(upper, fragment) {
			return true
		}
	}
	return false
}

func mergeSubprocessEnvExact(left, right map[string]struct{}) map[string]struct{} {
	merged := make(map[string]struct{}, len(left)+len(right))
	for name := range left {
		merged[name] = struct{}{}
	}
	for name := range right {
		merged[name] = struct{}{}
	}
	return merged
}
