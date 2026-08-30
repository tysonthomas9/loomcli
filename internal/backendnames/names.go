package backendnames

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
