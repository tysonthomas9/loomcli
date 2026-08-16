// Package roleprompts is the single home for the builtin-role prompt bodies and
// the on-disk prompt-file write helper shared across the serve plane.
//
// Two callers need this without importing each other:
//   - Repository Admission seeds the builtin `plan`/`task` role prompts at workspace
//     creation and backfills them at serve start;
//   - the webui roles handler writes operator-edited role prompts.
//
// Both must land the body at the same <workspace>/.loom/prompts/<name>.md
// location that the shared prompt loader (roles.ReadPromptBody) reads back, so
// the path/write logic lives here rather than in either caller. This package
// depends only on the standard library so any layer can import it.
package roleprompts

import (
	"crypto/sha256"
	"embed"
	"errors"
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

// ErrPromptFileConflict means an ensure-style write found the requested path
// with different content. Callers should surface this as a configuration
// conflict, never overwrite the winner.
var ErrPromptFileConflict = errors.New("role prompt file already exists with different content")

// PromptFileReceipt records whether an ensure call published a new immutable
// prompt. Published files are intentionally never unlinked as compensation:
// another concurrent role create may adopt the path after publication.
type PromptFileReceipt struct {
	Path    string
	created bool
}

// Created reports whether the ensure call published the prompt file.
func (r PromptFileReceipt) Created() bool {
	return r.created
}

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

// ImmutablePromptFilename derives a role-scoped, content-addressed filename
// from an operator-facing prompt filename. Explicit filenames are namespaced by
// role so two roles that request the same filename and body never share mutable
// storage. A Role points at one immutable prompt body, so deleted roles and
// losing concurrent creates can leave harmless files without blocking a later
// role generation or overwriting the winning role's prompt.
func ImmutablePromptFilename(roleName, filename, content string) string {
	base := strings.TrimSpace(filename)
	explicit := base != ""
	if base == "" {
		base = DefaultPromptFilename(roleName)
	}
	base = filepath.Base(base)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if stem == "" || stem == "." {
		stem = strings.TrimSpace(roleName)
		ext = ".md"
	}
	if explicit {
		roleName = strings.TrimSpace(roleName)
		roleNamespace := filepath.Base(roleName)
		if roleNamespace == "" ||
			roleNamespace == "." ||
			roleNamespace == string(filepath.Separator) {
			roleNamespace = "role"
		}
		roleSum := sha256.Sum256([]byte(roleName))
		if stem == roleNamespace {
			stem = fmt.Sprintf("%s.%x", roleNamespace, roleSum[:6])
		} else {
			// The role digest makes the namespace unambiguous even when a role
			// name and an explicit stem contain the same dot-delimited pieces.
			stem = fmt.Sprintf("%s.%s.%x", roleNamespace, stem, roleSum[:6])
		}
	}
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%s.%x%s", stem, sum[:6], ext)
}

// WritePromptFile writes a role prompt body to <wsPath>/.loom/prompts/<file>
// and returns the absolute path, mirroring the webui roles handler's original
// writeRolePrompt so both writers agree on layout. The filename defaults to
// <roleName>.md and is sanitized to its base to prevent path traversal out of
// the prompts directory.
func WritePromptFile(wsPath, roleName, filename, content string) (string, error) {
	path, err := resolvePromptFilePath(wsPath, roleName, filename)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write prompt file: %w", err)
	}
	return path, nil
}

// EnsurePromptFile creates a prompt file without overwriting a concurrent
// winner. An existing byte-identical file is idempotent; different content
// returns ErrPromptFileConflict. Role create and edit paths pair this with an
// immutable filename; WritePromptFile remains for conventional builtin
// seed/backfill and role-clone paths that allocate role-specific names.
func EnsurePromptFile(wsPath, roleName, filename, content string) (string, error) {
	receipt, err := EnsurePromptFileWithReceipt(wsPath, roleName, filename, content)
	return receipt.Path, err
}

// EnsurePromptFileWithReceipt is EnsurePromptFile with an ownership receipt
// for transactional callers that may need to compensate a later failure.
func EnsurePromptFileWithReceipt(wsPath, roleName, filename, content string) (PromptFileReceipt, error) {
	path, err := resolvePromptFilePath(wsPath, roleName, filename)
	if err != nil {
		return PromptFileReceipt{}, err
	}
	// Publish a fully-written temporary file with a no-replace hard link. Unlike
	// opening the destination O_EXCL and then writing it, this never exposes an
	// empty/partial winner that an identical concurrent ensure could misread as
	// a conflict.
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".ensure-*")
	if err != nil {
		return PromptFileReceipt{}, fmt.Errorf("create prompt temp file: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return PromptFileReceipt{}, fmt.Errorf("chmod prompt temp file: %w", err)
	}
	if _, err := temp.WriteString(content); err != nil {
		_ = temp.Close()
		return PromptFileReceipt{}, fmt.Errorf("write prompt file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return PromptFileReceipt{}, fmt.Errorf("close prompt file: %w", err)
	}
	if err := os.Link(tempPath, path); err == nil {
		return PromptFileReceipt{Path: path, created: true}, nil
	} else if !errors.Is(err, os.ErrExist) {
		return PromptFileReceipt{}, fmt.Errorf("publish prompt file: %w", err)
	}

	// path is produced by resolvePromptFilePath, which pins the read to the
	// workspace's .loom/prompts directory and reduces filename to filepath.Base.
	existing, err := os.ReadFile(path) //nolint:gosec // Sanitized workspace prompt path.
	if err != nil {
		return PromptFileReceipt{}, fmt.Errorf("read existing prompt file: %w", err)
	}
	if string(existing) != content {
		return PromptFileReceipt{}, fmt.Errorf("%w: %s", ErrPromptFileConflict, filepath.Base(path))
	}
	return PromptFileReceipt{Path: path}, nil
}

func resolvePromptFilePath(wsPath, roleName, filename string) (string, error) {
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
	return filepath.Join(dir, fname), nil
}
