package loomgit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
)

// WorkspaceSettingsStore supplies local workspace policy to Git operations.
type WorkspaceSettingsStore interface {
	AutoCommit(context.Context, string) (bool, error)
	SetAutoCommit(context.Context, string, bool) error
}

type CommitRequest struct {
	Workspace     string
	Paths         []string
	Message       string
	ChangeID      string
	Agent         string
	UserRequested bool
}

// Commit writes only named working-tree paths through a private index. The
// caller's index is never read or changed.
func Commit(ctx context.Context, repo string, settings WorkspaceSettingsStore, req CommitRequest) (string, error) {
	if err := validateCommitRequest(ctx, repo, settings, req); err != nil {
		return "", err
	}
	r, err := gitexec.New(repo, gitexec.Options{})
	if err != nil {
		return "", err
	}
	if err := refuseMerge(ctx, r, repo); err != nil {
		return "", err
	}
	for _, path := range req.Paths {
		if err := checkCommitPath(repo, path); err != nil {
			return "", err
		}
	}
	refBytes, err := r.Run(ctx, "symbolic-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("commit requires a branch: %w", err)
	}
	ref := strings.TrimSpace(string(refBytes))
	parentBytes, err := r.Run(ctx, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", err
	}
	parent := strings.TrimSpace(string(parentBytes))
	tree, err := treeForPaths(ctx, r, parent, req.Paths)
	if err != nil {
		return "", err
	}
	parentTree, err := r.Run(ctx, "rev-parse", parent+"^{tree}")
	if err != nil {
		return "", err
	}
	if tree == strings.TrimSpace(string(parentTree)) {
		return "", errors.New("commit has no changes")
	}
	message := strings.TrimSpace(req.Message) + "\n\nLoom-Change-Id: " + req.ChangeID + "\nLoom-Agent: " + req.Agent
	shaBytes, err := r.Run(ctx, "commit-tree", tree, "-p", parent, "-m", message)
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(shaBytes))
	if err := r.UpdateRef(ctx, ref, sha, parent); err != nil {
		return "", err
	}
	return sha, nil
}

func treeForPaths(ctx context.Context, r *gitexec.Runner, parent string, paths []string) (string, error) {
	tmp, err := os.CreateTemp("", "loom-commit-index-*")
	if err != nil {
		return "", err
	}
	index := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(index)
	env := map[string]string{"GIT_INDEX_FILE": index}
	if _, err = r.RunWithEnv(ctx, env, "read-tree", parent); err != nil {
		return "", err
	}
	args := []string{"add", "-A", "--"}
	for _, path := range paths {
		args = append(args, ":(literal)"+path)
	}
	if _, err = r.RunWithEnv(ctx, env, args...); err != nil {
		return "", err
	}
	treeBytes, err := r.RunWithEnv(ctx, env, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(treeBytes)), nil
}

func validateCommitRequest(ctx context.Context, repo string, settings WorkspaceSettingsStore, req CommitRequest) error {
	if repo == "" || settings == nil || req.Workspace == "" || len(req.Paths) == 0 ||
		strings.TrimSpace(req.Message) == "" || strings.TrimSpace(req.ChangeID) == "" || strings.TrimSpace(req.Agent) == "" {
		return errors.New("repo, workspace, paths, message, change ID and agent are required")
	}
	if strings.ContainsAny(req.ChangeID+req.Agent, "\r\n") {
		return errors.New("commit trailer values must be single-line")
	}
	enabled, err := settings.AutoCommit(ctx, req.Workspace)
	if err != nil {
		return err
	}
	if !enabled && !req.UserRequested {
		return errors.New("auto-commit is disabled for this workspace")
	}
	return nil
}

func refuseMerge(ctx context.Context, r *gitexec.Runner, repo string) error {
	gitPath, err := r.Run(ctx, "rev-parse", "--git-path", "MERGE_HEAD")
	if err != nil {
		return err
	}
	mergePath := strings.TrimSpace(string(gitPath))
	if !filepath.IsAbs(mergePath) {
		mergePath = filepath.Join(repo, mergePath)
	}
	if _, err := os.Stat(mergePath); err == nil {
		return errors.New("commit refused during merge")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func checkCommitPath(repo, path string) error {
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path || path == "." || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) || strings.ContainsRune(path, 0) {
		return fmt.Errorf("invalid commit path %q", path)
	}
	if info, err := os.Lstat(filepath.Join(repo, path)); err == nil && info.IsDir() {
		return fmt.Errorf("commit requires a file path: %q", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parts := strings.Split(filepath.Clean(path), string(filepath.Separator))
	for i, part := range parts {
		if part == ".git" {
			return fmt.Errorf("git metadata path refused: %q", path)
		}
		if i == len(parts)-1 {
			break
		}
		prefix := filepath.Join(append([]string{repo}, parts[:i+1]...)...)
		if info, err := os.Lstat(prefix); err == nil {
			if !info.IsDir() {
				return fmt.Errorf("commit path crosses non-directory: %q", path)
			}
			if _, err := os.Lstat(filepath.Join(prefix, ".git")); err == nil {
				return fmt.Errorf("submodule path refused: %q", path)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
