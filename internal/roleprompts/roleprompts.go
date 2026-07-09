// Package roleprompts is the single home for the builtin-role prompt bodies and
// the on-disk prompt-file write helper shared across the serve plane.
//
// Two callers need this without importing each other:
//   - workspacemgr seeds the builtin `plan`/`task` role prompts at workspace
//     creation and backfills them at serve start;
//   - the webui roles handler writes operator-edited role prompts.
//
// Both must land the body at the same <workspace>/.loom/prompts/<name>.md
// location that the shared prompt loader (roles.ReadPromptBody) reads back, so
// the path/write logic lives here rather than in either caller. This package
// depends only on the standard library so any layer can import it.
package roleprompts

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed prompts/ts-plan.md
//go:embed prompts/ts-task.md
var promptFS embed.FS

// TS-contract builtin prompt bodies. These are written for the
// prompt-agent → local-task-runner contract: the task is PRE-CLAIMED, a
// prepared worktree is provided, and there is NO self-claim loop and NO
// `loom stack publish`. They are deliberately NOT the Go daemon templates
// (internal/cli/agent/prompts/planning.md, task.md), whose self-claim/publish
// steps are the opposite contract. The Go plane never reads these bodies (its
// builtin spawn composes prompts from its own embedded templates), so editing
// them affects the TS/workflow plane only.
var (
	planBody = mustReadEmbedded("prompts/ts-plan.md")
	taskBody = mustReadEmbedded("prompts/ts-task.md")
)

// defaultBodies maps a builtin role name to its default TS-contract prompt body.
// Only the roles that drive a task run (plan, task) have a body; `lead` is a
// terminal role with no task-run prompt and is intentionally absent.
var defaultBodies = map[string]string{
	"plan": planBody,
	"task": taskBody,
}

func mustReadEmbedded(name string) string {
	data, err := promptFS.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("roleprompts: missing embedded asset %q: %v", name, err))
	}
	return string(data)
}

// DefaultPromptBody returns the embedded default prompt body for a builtin role
// (plan/task) and whether one exists. Roles without a default body (e.g. lead,
// or any custom role) return ("", false).
func DefaultPromptBody(roleName string) (string, bool) {
	body, ok := defaultBodies[strings.TrimSpace(roleName)]
	return body, ok
}

// HasDefaultPromptBody reports whether a builtin role ships a default body.
func HasDefaultPromptBody(roleName string) bool {
	_, ok := defaultBodies[strings.TrimSpace(roleName)]
	return ok
}

// BuiltinPromptRoleNames lists the builtin roles that ship a default prompt
// body, in a stable order (plan before task) for deterministic seeding and
// backfill.
func BuiltinPromptRoleNames() []string {
	return []string{"plan", "task"}
}

// DefaultPromptFilename is the conventional <name>.md filename a role's prompt
// body is written under in <workspace>/.loom/prompts.
func DefaultPromptFilename(roleName string) string {
	return strings.TrimSpace(roleName) + ".md"
}

// WritePromptFile writes a role prompt body to <wsPath>/.loom/prompts/<file>
// and returns the absolute path, mirroring the webui roles handler's original
// writeRolePrompt so both writers agree on layout. The filename defaults to
// <roleName>.md and is sanitized to its base to prevent path traversal out of
// the prompts directory.
func WritePromptFile(wsPath, roleName, filename, content string) (string, error) {
	wsPath = strings.TrimSpace(wsPath)
	if wsPath == "" {
		return "", fmt.Errorf("workspace path required to write role prompt")
	}
	fname := strings.TrimSpace(filename)
	if fname == "" {
		fname = strings.TrimSpace(roleName) + ".md"
	}
	fname = filepath.Base(fname)
	if fname == "." || fname == string(filepath.Separator) {
		return "", fmt.Errorf("invalid prompt filename")
	}
	dir := filepath.Join(wsPath, ".loom", "prompts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create prompt dir: %w", err)
	}
	path := filepath.Join(dir, fname)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write prompt file: %w", err)
	}
	return path, nil
}
