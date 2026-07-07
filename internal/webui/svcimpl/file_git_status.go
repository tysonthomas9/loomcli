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
	kind   string
	agent  string
	repo   string
	path   string
	prefix string
}

func (s *fileServiceImpl) GitStatusScoped(ctx context.Context, wsID string, scope service.FileScope, target, repo string) (service.FileGitStatusResult, error) {
	switch scope {
	case service.ScopeWorkspace:
		return s.workspaceGitStatus(ctx, wsID, target, repo)
	case service.ScopeRepo, service.ScopeAgent:
		root, err := s.resolveScopeRoot(wsID, scope, target, repo)
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

func (s *fileServiceImpl) workspaceGitStatus(ctx context.Context, wsID, target, repo string) (service.FileGitStatusResult, error) {
	if target != "" {
		return nil, service.ErrValidation("workspace scope does not take a target")
	}
	if repo != "" {
		return nil, service.ErrValidation("repo qualifier is only supported for agent scope")
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
	seen := make(map[string]struct{}, len(checkouts))
	for _, checkout := range checkouts {
		if err := ctx.Err(); err != nil {
			return nil, service.ErrTimeout("git status canceled")
		}
		if _, ok := seen[checkout.path]; ok {
			continue
		}
		seen[checkout.path] = struct{}{}
		if !checkoutExists(wsRoot, checkout.path) {
			continue
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
	return workspaceFileCheckouts(wsID, wsRoot, ws)
}

func workspaceFileCheckouts(_ string, wsRoot string, ws *ops.WorkspaceData) []gitStatusCheckout {
	if ws == nil {
		return nil
	}
	checkouts := make([]gitStatusCheckout, 0, len(ws.Repos)+len(ws.Agents)*len(ws.Repos))
	for _, repo := range ws.Repos {
		if checkout, ok := workspaceCheckoutWithinRoot(wsRoot, repoCheckoutPath(wsRoot, repo)); ok {
			checkout.kind = "repo"
			checkout.repo = repo.Name
			checkouts = append(checkouts, checkout)
		}
	}
	for _, agent := range ws.Agents {
		for _, repo := range agentCheckoutRepos(ws.Repos, agent) {
			path := filepath.Join(wsRoot, "worktrees", repo.Name, agent.Name)
			checkout, ok := workspaceCheckoutWithinRoot(wsRoot, path)
			if !ok {
				continue
			}
			checkout.kind = "agent"
			checkout.agent = agent.Name
			checkout.repo = repo.Name
			checkouts = append(checkouts, checkout)
		}
	}
	return checkouts
}

func (s *fileServiceImpl) ListFileCheckouts(ctx context.Context, wsID string) (*service.FileCheckoutsResult, error) {
	ws, err := s.resolveWorkspaceData(wsID)
	if err != nil {
		return nil, err
	}
	wsRoot := ws.Path
	checkouts := workspaceFileCheckouts(wsID, wsRoot, ws)
	out := make([]service.FileCheckout, 0, len(checkouts))
	for _, checkout := range checkouts {
		if err := ctx.Err(); err != nil {
			return nil, service.ErrTimeout("checkout listing canceled")
		}
		item := service.FileCheckout{
			Kind:  checkout.kind,
			Agent: checkout.agent,
			Repo:  checkout.repo,
		}
		if checkoutExists(wsRoot, checkout.path) {
			item.Exists = true
			status, err := s.fileOps.GitStatusPorcelain(checkout.path)
			if err != nil {
				return nil, service.ErrInternal("failed to run git status", err)
			}
			item.ChangeCount = len(status)
			if branch, err := s.fileOps.GetCurrentBranch(checkout.path); err == nil {
				item.Branch = branch
			}
		}
		out = append(out, item)
	}
	return &service.FileCheckoutsResult{Checkouts: out}, nil
}

func repoCheckoutPath(wsRoot string, repo ops.WorkspaceRepo) string {
	if repo.Path != "" {
		return repo.Path
	}
	return filepath.Join(wsRoot, repo.Name)
}

func agentCheckoutRepos(repos []ops.WorkspaceRepo, agent ops.WorkspaceAgentInfo) []ops.WorkspaceRepo {
	if len(agent.Repos) == 0 && len(agent.RepoGroups) == 0 {
		return append([]ops.WorkspaceRepo(nil), repos...)
	}
	allowed := make(map[string]bool)
	for _, name := range agent.Repos {
		allowed[name] = true
	}
	for _, group := range agent.RepoGroups {
		for _, repo := range repos {
			for _, repoGroup := range repo.Groups {
				if repoGroup == group {
					allowed[repo.Name] = true
					break
				}
			}
		}
	}
	out := make([]ops.WorkspaceRepo, 0, len(allowed))
	for _, repo := range repos {
		if allowed[repo.Name] {
			out = append(out, repo)
		}
	}
	return out
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
	rel, err := filepath.Rel(absRoot, absCheckout)
	if err != nil || rel == "." {
		return gitStatusCheckout{path: absCheckout}, true
	}
	return gitStatusCheckout{path: absCheckout, prefix: filepath.ToSlash(rel)}, true
}

func checkoutExists(wsRoot, checkoutPath string) bool {
	if checkoutPath == "" {
		return false
	}
	absRoot, err := filepath.Abs(wsRoot)
	if err != nil {
		return false
	}
	absCheckout, err := filepath.Abs(checkoutPath)
	if err != nil {
		return false
	}
	if err := webuilog.ValidatePathWithinDir(absCheckout, absRoot); err != nil {
		return false
	}
	if err := validateNoSymlinkComponents(absRoot, absCheckout); err != nil {
		return false
	}
	return validateGitCheckoutRoot(absCheckout) == nil
}
