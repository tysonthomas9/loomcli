package svcimpl

import (
	"context"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/ops"
	webuilog "github.com/tysonthomas9/loomcli/internal/webui/log"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type gitStatusCheckout struct {
	path   string
	prefix string
}

func (s *fileServiceImpl) GitStatusScoped(ctx context.Context, wsID string, scope service.FileScope, target string) (service.FileGitStatusResult, error) {
	switch scope {
	case service.ScopeWorkspace:
		return s.workspaceGitStatus(ctx, wsID, target)
	case service.ScopeRepo, service.ScopeAgent:
		root, err := s.resolveScopeRoot(wsID, scope, target)
		if err != nil {
			return nil, err
		}
		if err := validateGitCheckoutRoot(root); err != nil {
			return nil, err
		}
		status, err := s.fileOps.GitStatusPorcelain(root)
		if err != nil {
			return nil, service.ErrInternal("failed to run git status", err)
		}
		return service.FileGitStatusResult(status), nil
	default:
		return nil, service.ErrValidation("unsupported scope " + string(scope))
	}
}

func (s *fileServiceImpl) workspaceGitStatus(ctx context.Context, wsID, target string) (service.FileGitStatusResult, error) {
	if target != "" {
		return nil, service.ErrValidation("workspace scope does not take a target")
	}
	wsRoot, err := s.resolveWorkspaceScopeRoot(wsID, target)
	if err != nil {
		return nil, err
	}
	ws, err := s.resolveWorkspaceData(wsID)
	if err != nil {
		return nil, err
	}
	checkouts := s.workspaceGitStatusCheckouts(wsID, wsRoot, ws)
	result := service.FileGitStatusResult{}
	for _, checkout := range checkouts {
		if err := ctx.Err(); err != nil {
			return nil, service.ErrTimeout("git status canceled")
		}
		status, err := s.fileOps.GitStatusPorcelain(checkout.path)
		if err != nil {
			return nil, service.ErrInternal("failed to run git status", err)
		}
		mergePrefixedGitStatus(result, checkout.prefix, status)
	}
	return result, nil
}

func (s *fileServiceImpl) workspaceGitStatusCheckouts(wsID, wsRoot string, ws *ops.WorkspaceData) []gitStatusCheckout {
	if ws == nil {
		return nil
	}
	checkouts := make([]gitStatusCheckout, 0, len(ws.Repos)+len(ws.Agents))
	seen := make(map[string]struct{})
	add := func(path string) {
		checkout, ok := workspaceCheckoutWithinRoot(wsRoot, path)
		if !ok {
			return
		}
		if _, exists := seen[checkout.path]; exists {
			return
		}
		seen[checkout.path] = struct{}{}
		checkouts = append(checkouts, checkout)
	}

	for _, repo := range ws.Repos {
		path := repo.Path
		if path == "" {
			path = filepath.Join(wsRoot, repo.Name)
		}
		add(path)
	}
	for _, agent := range ws.Agents {
		wt, err := s.fileOps.ResolveAgentWorktree(wsID, agent.Name)
		if err != nil || wt == nil {
			continue
		}
		add(wt.Path)
	}
	return checkouts
}

func mergePrefixedGitStatus(dst service.FileGitStatusResult, prefix string, status map[string]string) {
	for path, xy := range status {
		if prefix != "" {
			path = prefix + "/" + path
		}
		dst[path] = xy
	}
}

func validateGitCheckoutRoot(root string) error {
	fi, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return service.ErrNotFound("git checkout not found")
		}
		return service.ErrInternal("failed to stat git checkout", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return service.ErrForbidden("refusing to follow symlink")
	}
	if !fi.IsDir() {
		return service.ErrValidation("git checkout root is not a directory")
	}
	gitInfo, err := os.Lstat(filepath.Join(root, ".git"))
	if err != nil {
		if os.IsNotExist(err) {
			return service.ErrNotFound("git checkout not found")
		}
		return service.ErrInternal("failed to stat git metadata", err)
	}
	if gitInfo.Mode()&os.ModeSymlink != 0 {
		return service.ErrForbidden("refusing to follow symlink")
	}
	return nil
}

func workspaceCheckoutWithinRoot(wsRoot, checkoutPath string) (gitStatusCheckout, bool) {
	if checkoutPath == "" {
		return gitStatusCheckout{}, false
	}
	absRoot, err := filepath.Abs(wsRoot)
	if err != nil {
		return gitStatusCheckout{}, false
	}
	absCheckout, err := filepath.Abs(checkoutPath)
	if err != nil {
		return gitStatusCheckout{}, false
	}
	if err := webuilog.ValidatePathWithinDir(absCheckout, absRoot); err != nil {
		return gitStatusCheckout{}, false
	}
	if err := validateNoSymlinkComponents(absRoot, absCheckout); err != nil {
		return gitStatusCheckout{}, false
	}
	if err := validateGitCheckoutRoot(absCheckout); err != nil {
		return gitStatusCheckout{}, false
	}
	rel, err := filepath.Rel(absRoot, absCheckout)
	if err != nil || rel == "." {
		return gitStatusCheckout{path: absCheckout}, true
	}
	return gitStatusCheckout{path: absCheckout, prefix: filepath.ToSlash(rel)}, true
}
