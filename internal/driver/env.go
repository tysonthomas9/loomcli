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

// trustedLocalProviderCredentials are the provider-credential names in the
// shared contract. The local task runner inherits backend credentials only.
// This widening is STRICTLY scoped to the
// local-task-runner entrypoint — Daytona/remote runners keep the strict filter
// in scopedSubprocessBaseEnv (which drops every one of these) so a credential
// never leaks into a remote sandbox.
//
// This map is NOT kept in step by hand. It is the `provider_credentials` role of
// the vendored contract testdata/sensitive-env-names.json (mirrored byte-for-byte
// from meta-harness's contract/sensitive-env-names.json), and
// sensitive_env_contract_test.go asserts set equality against it — as does
// internal/workflows/builtin/daytona-task-runner.test.mjs for the leak probe that
// must enumerate the same names. To change the list, edit the artifact in
// meta-harness, re-run scripts/sync-sensitive-env-names.sh --to <this repo> there,
// and land both PRs. See contract/README.md in that repo.
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
	// Kept in the shared sensitive-name contract for sandbox leak probes.
	// GitHub credentials are never forwarded to a task runner.
	"GITHUB_TOKEN": {},
	"GH_TOKEN":     {},
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
		if _, allowed := trustedLocalProviderCredentials[strings.TrimSpace(name)]; allowed && name != "GITHUB_TOKEN" && name != "GH_TOKEN" {
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
