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

	// Test helper marker for subprocess-backed driver tests.
	"LOOM_HOST_BRIDGE_HELPER": {},
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

func driverRuntimeBaseEnv(env []string) []string {
	return scopedSubprocessBaseEnv(env)
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
