// Package backendnames holds the canonical string ids of the agent backends (the
// AI CLIs loom drives) that other packages branch on by name: "claude" and "codex".
// It is the leaf definition that internal/cli/backends re-exports, so preflight,
// doctor, and session-finalize compare against the same literals. Not issue backends.
package backendnames

const (
	Claude = "claude"
	Codex  = "codex"
)
