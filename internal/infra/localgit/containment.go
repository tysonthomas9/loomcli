package localgit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// canonicalCheckoutTarget rejects link-based traversal and returns the real
// prospective target identity used by both Source Control and Connectors.
//
//nolint:cyclop,funlen,gocognit // Keep symlink, ancestor, device, and containment checks in one fail-closed checkout-target proof.
func canonicalCheckoutTarget(
	ctx context.Context,
	workspacePath string,
	targetPath string,
) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if workspacePath == "" ||
		!filepath.IsAbs(workspacePath) ||
		filepath.Clean(workspacePath) != workspacePath {
		return "", fmt.Errorf("workspace path must be absolute and canonical")
	}
	if targetPath == "" ||
		!filepath.IsAbs(targetPath) ||
		filepath.Clean(targetPath) != targetPath {
		return "", fmt.Errorf("target path must be absolute and canonical")
	}
	if targetPath == workspacePath || !pathContains(workspacePath, targetPath) {
		return "", fmt.Errorf("target path must be strictly contained by the workspace")
	}

	workspaceInfo, err := os.Lstat(workspacePath)
	if err != nil {
		return "", fmt.Errorf("inspect workspace path: %w", err)
	}
	if workspaceInfo.Mode()&os.ModeSymlink != 0 || !workspaceInfo.IsDir() {
		return "", fmt.Errorf("workspace path must be a real directory")
	}
	resolvedWorkspace, err := filepath.EvalSymlinks(workspacePath)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	resolvedWorkspace = filepath.Clean(resolvedWorkspace)

	targetParent := filepath.Dir(targetPath)
	parentRelative, err := filepath.Rel(workspacePath, targetParent)
	if err != nil {
		return "", fmt.Errorf("resolve target parent: %w", err)
	}
	current := workspacePath
	if parentRelative != "." {
		for _, component := range strings.Split(parentRelative, string(filepath.Separator)) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			current = filepath.Join(current, component)
			info, statErr := os.Lstat(current)
			if statErr != nil {
				return "", fmt.Errorf("inspect target parent: %w", statErr)
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return "", fmt.Errorf("target parent must contain only real directories")
			}
		}
	}
	resolvedParent, err := filepath.EvalSymlinks(targetParent)
	if err != nil {
		return "", fmt.Errorf("resolve target parent: %w", err)
	}
	resolvedParent = filepath.Clean(resolvedParent)
	if resolvedParent != resolvedWorkspace && !pathContains(resolvedWorkspace, resolvedParent) {
		return "", fmt.Errorf("resolved target parent escapes the workspace")
	}

	canonicalTarget := filepath.Join(resolvedParent, filepath.Base(targetPath))
	if !pathContains(resolvedWorkspace, canonicalTarget) {
		return "", fmt.Errorf("resolved target escapes the workspace")
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect target path: %w", err)
	}
	if err == nil {
		if targetInfo.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("target path must not be a symlink")
		}
		resolvedTarget, resolveErr := filepath.EvalSymlinks(targetPath)
		if resolveErr != nil {
			return "", fmt.Errorf("resolve target path: %w", resolveErr)
		}
		resolvedTarget = filepath.Clean(resolvedTarget)
		if resolvedTarget != canonicalTarget ||
			!pathContains(resolvedWorkspace, resolvedTarget) {
			return "", fmt.Errorf("resolved target identity changed")
		}
		canonicalTarget = resolvedTarget
	}
	return canonicalTarget, nil
}

func pathContains(root string, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative == "." ||
		(relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
