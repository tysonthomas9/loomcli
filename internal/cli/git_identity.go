package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

var errNotGitCheckout = errors.New("not a git repository")

type gitCheckoutIdentity struct {
	TopLevel  string
	CommonDir string
}

// inspectGitCheckout resolves the checked-out worktree root and shared common
// git dir for an existing path.
func inspectGitCheckout(path string) (*gitCheckoutIdentity, error) {
	if path == "" {
		return nil, fmt.Errorf("path is empty")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute path: %w", err)
	}
	absPath, err = filepath.EvalSymlinks(absPath)
	if err != nil {
		return nil, fmt.Errorf("resolve symlinks: %w", err)
	}

	topLevelResult := execCommand(absPath, "git", "rev-parse", "--show-toplevel")
	if topLevelResult.Err != nil {
		if looksLikeNotGitRepo(topLevelResult.Stderr) || looksLikeNotGitRepo(topLevelResult.Err.Error()) {
			return nil, errNotGitCheckout
		}
		return nil, fmt.Errorf("resolve git top-level: %w", topLevelResult.Err)
	}

	commonDirResult := execCommand(absPath, "git", "rev-parse", "--git-common-dir")
	if commonDirResult.Err != nil {
		if looksLikeNotGitRepo(commonDirResult.Stderr) || looksLikeNotGitRepo(commonDirResult.Err.Error()) {
			return nil, errNotGitCheckout
		}
		return nil, fmt.Errorf("resolve git common dir: %w", commonDirResult.Err)
	}

	topLevel, err := normalizeGitPath(absPath, topLevelResult.Stdout)
	if err != nil {
		return nil, fmt.Errorf("normalize git top-level: %w", err)
	}
	commonDir, err := normalizeGitPath(absPath, commonDirResult.Stdout)
	if err != nil {
		return nil, fmt.Errorf("normalize git common dir: %w", err)
	}

	return &gitCheckoutIdentity{
		TopLevel:  topLevel,
		CommonDir: commonDir,
	}, nil
}

func normalizeGitPath(baseDir, raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	path = filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	return path, nil
}

func looksLikeNotGitRepo(msg string) bool {
	msg = strings.ToLower(msg)
	return strings.Contains(msg, "not a git repository")
}
