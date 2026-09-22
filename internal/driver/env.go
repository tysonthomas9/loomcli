package driver

import "strings"

var subprocessEnvAllowExact = map[string]struct{}{
	"PATH":    {},
	"HOME":    {},
	"PWD":     {},
	"OLDPWD":  {},
	"TMPDIR":  {},
	"TMP":     {},
	"TEMP":    {},
	"TERM":    {},
	"USER":    {},
	"LOGNAME": {},
	"SHELL":   {},
	"TZ":      {},
	"LANG":    {},

	"LOOM_CONFIG_DIR":       {},
	"LOOM_FLUE_AGENT_MODEL": {},
	// The workspace runtime dir is a non-secret host path; the scout leaf uses
	// it as its explicit workspace-root anchor (falling back to
	// LOOM_WORKTREE_PATH, with the "." fallback refused).
	"LOOM_WORKSPACE_RUNTIME_DIR": {},

	// Test helper marker for subprocess-backed driver tests.
	"LOOM_HOST_BRIDGE_HELPER": {},
	TaskRunnerCommandJSONEnv:  {},
	TaskRunnerCommandEnv:      {},
}

var subprocessEnvAllowPrefixes = []string{
	"LC_",
}

var subprocessEnvSensitiveExact = map[string]struct{}{
	"GITHUB_TOKEN":                   {},
	"GH_TOKEN":                       {},
	"GITLAB_TOKEN":                   {},
	"OPENAI_API_KEY":                 {},
	"ANTHROPIC_API_KEY":              {},
	"GEMINI_API_KEY":                 {},
	"GOOGLE_API_KEY":                 {},
	"GOOGLE_APPLICATION_CREDENTIALS": {},
	"CODEX_API_KEY":                  {},
	"CURSOR_API_KEY":                 {},
	"LOOM_FLEET_DB_URL":              {},
	"LOOM_FLEET_DB_API_KEY":          {},
	"LOOM_FLEET_DB_ACTOR":            {},
	"LOOM_FLEET_API_KEY":             {},
	"LOOM_FLEETDB_REDIS_URL":         {},
	"LOOM_FLEETDB_REDIS_PASSWORD":    {},
	"LOOM_FLEET_DB_REDIS_ADDR":       {},
	"LOOM_FLEET_DB_REDIS_PASSWORD":   {},
	"LOOM_TASK_RUN_LEASE_TOKEN":      {},
	"LOOM_RUNNER_LEASE_TOKEN":        {},
	"LOOM_AGENT_LEASE_TOKEN":         {},
	"LOOM_WORKER_TOKEN":              {},
}

var subprocessEnvSensitivePrefixes = []string{
	"AWS_",
	"AZURE_",
	"GCP_",
	"GOOGLE_",
	"FLEET_",
	"GIT_CONFIG_",
}

var subprocessEnvSensitiveFragments = []string{
	"SECRET",
	"TOKEN",
	"PASSWORD",
	"PRIVATE_KEY",
	"ACCESS_KEY",
	"API_KEY",
}

// trustedLocalProviderCredentials are the provider-credential env vars the
// local task runner is allowed to inherit so the backend CLI authenticates
// exactly as local tooling does (§4.3). This widening is STRICTLY scoped to the
// local-task-runner entrypoint — Daytona/remote runners keep the strict filter
// in scopedSubprocessBaseEnv (which treats every one of these as sensitive) so
// a credential never leaks into a remote sandbox.
var trustedLocalProviderCredentials = map[string]struct{}{
	"ANTHROPIC_API_KEY": {},
	// claude-code's long-lived OAuth token (`claude setup-token`); the headless
	// equivalent of a ~/.claude login, so the local runner must inherit it too.
	"CLAUDE_CODE_OAUTH_TOKEN":        {},
	"OPENAI_API_KEY":                 {},
	"CODEX_API_KEY":                  {},
	"CODEX_HOME":                     {},
	"GEMINI_API_KEY":                 {},
	"GOOGLE_API_KEY":                 {},
	"GOOGLE_APPLICATION_CREDENTIALS": {},
	"CURSOR_API_KEY":                 {},
	// GitHub tokens enable the local runner's opt-in pull-request delivery.
	// They remain in subprocessEnvSensitiveExact so the strict filter still
	// denies them to Daytona/remote runners; localTaskRunnerBaseEnv adds them
	// back ONLY for the local-task-runner entrypoint.
	"GITHUB_TOKEN": {},
	"GH_TOKEN":     {},
}

func driverRuntimeBaseEnv(env []string) []string {
	return scopedSubprocessBaseEnv(env)
}

// localTaskRunnerBaseEnv is the trusted-local superset of the strict driver
// allowlist: it keeps everything scopedSubprocessBaseEnv admits (PATH/HOME/…)
// and additionally admits the provider-credential allowlist so the local
// backend CLI can authenticate. It is used ONLY for the local-task-runner
// entrypoint.
func localTaskRunnerBaseEnv(env []string) []string {
	out := scopedSubprocessBaseEnv(env)
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, allowed := trustedLocalProviderCredentials[strings.TrimSpace(name)]; allowed {
			out = append(out, entry)
		}
	}
	return out
}

func scopedSubprocessBaseEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || !subprocessEnvAllowed(name) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func subprocessEnvAllowed(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || subprocessEnvSensitive(name) {
		return false
	}
	if _, ok := subprocessEnvAllowExact[name]; ok {
		return true
	}
	for _, prefix := range subprocessEnvAllowPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func subprocessEnvSensitive(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	if upper == "" {
		return true
	}
	if _, ok := subprocessEnvSensitiveExact[upper]; ok {
		return true
	}
	for _, prefix := range subprocessEnvSensitivePrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	for _, fragment := range subprocessEnvSensitiveFragments {
		if strings.Contains(upper, fragment) {
			return true
		}
	}
	return false
}
