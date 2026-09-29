package backendnames

import "strings"

const (
	Claude   = "claude"
	Codex    = "codex"
	OpenCode = "opencode"
	Gemini   = "gemini"
	Cursor   = "cursor"
)

var localTaskRunnerBackends = []string{
	Claude,
	Codex,
	OpenCode,
	Gemini,
	Cursor,
}

var controlledLeadBackends = []string{
	Codex,
	Claude,
	Gemini,
	OpenCode,
	Cursor,
}

// IsLocalTaskRunnerBackend reports whether name can be launched by the
// bundled local task runner.
func IsLocalTaskRunnerBackend(name string) bool {
	for _, backend := range localTaskRunnerBackends {
		if name == backend {
			return true
		}
	}
	return false
}

// LocalTaskRunnerBackends returns the backends supported by the bundled
// local task runner in its canonical display order.
func LocalTaskRunnerBackends() []string {
	return append([]string(nil), localTaskRunnerBackends...)
}

// IsControlledLeadBackend reports whether name has a controlled interactive
// lead runtime that supports queued message delivery.
func IsControlledLeadBackend(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, backend := range controlledLeadBackends {
		if name == backend {
			return true
		}
	}
	return false
}

// ControlledLeadBackends returns the controlled lead backends in canonical
// display order.
func ControlledLeadBackends() []string {
	return append([]string(nil), controlledLeadBackends...)
}
