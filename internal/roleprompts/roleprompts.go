// Package roleprompts owns immutable workspace-local role prompt publication
// and validated prompt-body reads.
package roleprompts

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrPromptFileConflict means a content-addressed destination already exists
// with bytes other than the requested body.
var ErrPromptFileConflict = errors.New("role prompt file already exists with different content")

// ErrExternal marks a prompt path that resolves outside its workspace.
var ErrExternal = errors.New("role prompt path is external to the workspace")

// ExternalError identifies a prompt path rejected by the workspace containment
// guard. It never exposes the contents of the rejected path.
type ExternalError struct {
	PromptFile string
}

func (e *ExternalError) Error() string {
	return fmt.Sprintf("role prompt path %q is external to the workspace", e.PromptFile)
}

func (e *ExternalError) Unwrap() error { return ErrExternal }

// Publish atomically publishes body to a content-addressed immutable file at
// <workspaceDir>/.loom/prompts/<role>.<sha-prefix>.md. Identical content is
// idempotent and an existing destination is never rewritten in place.
func Publish(workspaceDir, roleName, body string) (string, error) {
	dir, err := promptDir(workspaceDir)
	if err != nil {
		return "", err
	}
	stem, err := roleStem(roleName)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(body))
	path := filepath.Join(dir, fmt.Sprintf("%s.%x.md", stem, sum[:6]))

	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".publish-*")
	if err != nil {
		return "", fmt.Errorf("create role prompt temp file: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return "", fmt.Errorf("chmod role prompt temp file: %w", err)
	}
	if _, err := temp.WriteString(body); err != nil {
		_ = temp.Close()
		return "", fmt.Errorf("write role prompt temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return "", fmt.Errorf("close role prompt temp file: %w", err)
	}

	if err := os.Link(tempPath, path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("publish role prompt file: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect existing role prompt file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s", ErrPromptFileConflict, filepath.Base(path))
	}
	existing, err := os.ReadFile(path) //nolint:gosec // path is pinned to the validated prompt directory.
	if err != nil {
		return "", fmt.Errorf("read existing role prompt file: %w", err)
	}
	if string(existing) != body {
		return "", fmt.Errorf("%w: %s", ErrPromptFileConflict, filepath.Base(path))
	}
	return path, nil
}

// ReadValidated returns the prompt body only when promptFile resolves inside
// workspaceDir. Absolute outside paths and symlinks escaping the workspace are
// rejected with an *ExternalError before any file contents are returned.
func ReadValidated(workspaceDir, promptFile string) (string, error) {
	rootAbs, rootResolved, err := workspaceRoots(workspaceDir)
	if err != nil {
		return "", err
	}
	promptFile = strings.TrimSpace(promptFile)
	if promptFile == "" {
		return "", fmt.Errorf("role prompt file is required")
	}
	candidate := promptFile
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(rootAbs, candidate)
	}
	candidate, err = filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return "", fmt.Errorf("resolve role prompt path: %w", err)
	}
	if !within(rootAbs, candidate) && !within(rootResolved, candidate) {
		return "", &ExternalError{PromptFile: promptFile}
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve role prompt file: %w", err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve role prompt file: %w", err)
	}
	if !within(rootResolved, resolved) {
		return "", &ExternalError{PromptFile: promptFile}
	}
	data, err := os.ReadFile(resolved) //nolint:gosec // resolved path is contained by the evaluated workspace root.
	if err != nil {
		return "", fmt.Errorf("read role prompt file: %w", err)
	}
	return string(data), nil
}

func roleStem(roleName string) (string, error) {
	roleName = strings.TrimSpace(roleName)
	if roleName == "" {
		return "", fmt.Errorf("role name is required")
	}
	stem := filepath.Base(roleName)
	if stem == "." || stem == string(filepath.Separator) || stem != roleName {
		return "", fmt.Errorf("invalid role name %q", roleName)
	}
	return stem, nil
}

func promptDir(workspaceDir string) (string, error) {
	rootAbs, rootResolved, err := workspaceRoots(workspaceDir)
	if err != nil {
		return "", err
	}
	loomDir := filepath.Join(rootAbs, ".loom")
	if err := ensureRealDir(loomDir); err != nil {
		return "", err
	}
	dir := filepath.Join(loomDir, "prompts")
	if err := ensureRealDir(dir); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolve role prompt directory: %w", err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve role prompt directory: %w", err)
	}
	if !within(rootResolved, resolved) {
		return "", &ExternalError{PromptFile: dir}
	}
	return resolved, nil
}

func workspaceRoots(workspaceDir string) (string, string, error) {
	workspaceDir = strings.TrimSpace(workspaceDir)
	if workspaceDir == "" {
		return "", "", fmt.Errorf("workspace directory is required")
	}
	abs, err := filepath.Abs(filepath.Clean(workspaceDir))
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace directory: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", "", fmt.Errorf("stat workspace directory: %w", err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("workspace path is not a directory")
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace directory: %w", err)
	}
	return abs, resolved, nil
}

func ensureRealDir(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o755); err != nil {
			return fmt.Errorf("create role prompt directory: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect role prompt directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("role prompt directory must be a real directory: %s", path)
	}
	return nil
}

func within(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
