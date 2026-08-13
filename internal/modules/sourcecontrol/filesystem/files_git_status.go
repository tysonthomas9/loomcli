package filesystem

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type gitStatusCheckout struct {
	kind   string
	agent  string
	repo   string
	path   string
	prefix string
}

const (
	workspaceGitFanoutTimeout = 10 * time.Second
	workspaceGitConcurrency   = 4
)

func (s *fileServiceImpl) GitStatusScoped(ctx context.Context, wsID string, scope FileScope, target, repo string) (FileGitStatusResult, error) {
	return s.gitStatusScoped(ctx, wsID, scope, target, repo, fileAccess{})
}

func (s *fileServiceImpl) gitStatusScoped(ctx context.Context, wsID string, scope FileScope, target, repo string, access fileAccess) (FileGitStatusResult, error) {
	switch scope {
	case ScopeWorkspace:
		return s.workspaceGitStatus(ctx, wsID, target, repo, access)
	case ScopeRepo, ScopeAgent:
		root, err := s.resolveScopedRoot(wsID, scope, target, repo)
		if err != nil {
			return FileGitStatusResult{}, err
		}
		defer root.Close()
		if err := validateGitCheckoutRoot(root.path); err != nil {
			return FileGitStatusResult{}, err
		}
		gitCtx, err := withGitCheckoutIdentity(ctx, root.path, root)
		if err != nil {
			return FileGitStatusResult{}, err
		}
		status, err := s.fileOps.GitStatusPorcelain(gitCtx, root.path)
		if err != nil {
			return FileGitStatusResult{}, mapGitInspectionError("failed to run git status", err)
		}
		return FileGitStatusResult{
			Status:  filterSensitiveGitStatus(access, status.Entries),
			Partial: status.Partial, LimitHit: status.LimitHit,
			Errors: []FileCheckoutError{},
		}, nil
	default:
		return FileGitStatusResult{}, newInvalid("unsupported scope " + string(scope))
	}
}

func (s *fileServiceImpl) workspaceGitStatus(ctx context.Context, wsID, target, repo string, access fileAccess) (FileGitStatusResult, error) {
	if target != "" {
		return FileGitStatusResult{}, newInvalid("workspace scope does not take a target")
	}
	if repo != "" {
		return FileGitStatusResult{}, newInvalid("repo qualifier is only supported for agent scope")
	}
	wsRoot, err := s.resolveWorkspaceScopeRoot(wsID, target)
	if err != nil {
		return FileGitStatusResult{}, err
	}
	ws, err := s.resolveWorkspaceData(wsID)
	if err != nil {
		return FileGitStatusResult{}, err
	}
	checkouts := workspaceFileCheckouts(wsID, wsRoot, ws)
	result := &FileGitStatusResult{Status: map[string]string{}, Errors: []FileCheckoutError{}}
	seen := make(map[string]struct{}, len(checkouts))
	unique := make([]gitStatusCheckout, 0, len(checkouts))
	for _, checkout := range checkouts {
		if _, ok := seen[checkout.path]; ok {
			continue
		}
		seen[checkout.path] = struct{}{}
		if !checkoutPathPresent(wsRoot, checkout.path) {
			continue
		}
		unique = append(unique, checkout)
	}
	for _, item := range s.inspectWorkspaceCheckouts(ctx, unique, false) {
		if item.err != nil {
			result.Partial = true
			result.Errors = append(result.Errors, checkoutError(item.checkout, item.err))
			continue
		}
		result.Partial = result.Partial || item.status.Partial
		result.LimitHit = result.LimitHit || item.status.LimitHit
		mergePrefixedGitStatus(access, result.Status, item.checkout.prefix, item.status.Entries)
	}
	sortCheckoutErrors(result.Errors)
	return *result, nil
}

func workspaceFileCheckouts(_ string, wsRoot string, ws *WorkspaceTopology) []gitStatusCheckout {
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

func (s *fileServiceImpl) ListFileCheckouts(ctx context.Context, wsID string) (*FileCheckoutsResult, error) {
	return s.listFileCheckouts(ctx, wsID, fileAccess{})
}

func (s *fileServiceImpl) listFileCheckouts(ctx context.Context, wsID string, access fileAccess) (*FileCheckoutsResult, error) {
	ws, err := s.resolveWorkspaceData(wsID)
	if err != nil {
		return nil, err
	}
	wsRoot := ws.Path
	checkouts := workspaceFileCheckouts(wsID, wsRoot, ws)
	inspectable := presentGitCheckouts(wsRoot, checkouts)
	items := s.inspectWorkspaceCheckouts(ctx, inspectable, true)
	byPath := make(map[string]workspaceGitInspection, len(items))
	for _, item := range items {
		byPath[item.checkout.path] = item
	}
	out := make([]FileCheckout, 0, len(checkouts))
	result := &FileCheckoutsResult{Errors: []FileCheckoutError{}}
	for _, checkout := range checkouts {
		item := FileCheckout{
			Kind:  checkout.kind,
			Agent: checkout.agent,
			Repo:  checkout.repo,
		}
		if checkoutPathPresent(wsRoot, checkout.path) {
			item.Exists = true
			inspection := byPath[checkout.path]
			if inspection.err != nil {
				item.StatusError = true
				item.Error = inspection.err.Error()
				result.Partial = true
				result.Errors = append(result.Errors, checkoutError(checkout, inspection.err))
				out = append(out, item)
				continue
			}
			item.Partial, item.LimitHit = inspection.status.Partial, inspection.status.LimitHit
			result.Partial = result.Partial || item.Partial
			result.LimitHit = result.LimitHit || item.LimitHit
			item.ChangeCount = len(filterSensitiveGitStatus(access, inspection.status.Entries))
			item.Branch = inspection.branch
			if inspection.branchErr != nil {
				item.Partial = true
				item.Error = inspection.branchErr.Error()
				result.Partial = true
				result.Errors = append(result.Errors, checkoutError(checkout, inspection.branchErr))
			}
		}
		out = append(out, item)
	}
	result.Checkouts = out
	sortCheckoutErrors(result.Errors)
	return result, nil
}

func presentGitCheckouts(wsRoot string, checkouts []gitStatusCheckout) []gitStatusCheckout {
	out := make([]gitStatusCheckout, 0, len(checkouts))
	for _, checkout := range checkouts {
		if checkoutPathPresent(wsRoot, checkout.path) {
			out = append(out, checkout)
		}
	}
	return out
}

type workspaceGitInspection struct {
	checkout  gitStatusCheckout
	status    GitFileStatusResult
	branch    string
	branchErr error
	err       error
}

func (s *fileServiceImpl) inspectWorkspaceCheckouts(ctx context.Context, checkouts []gitStatusCheckout, includeBranch bool) []workspaceGitInspection {
	ctx, cancel := context.WithTimeout(ctx, workspaceGitFanoutTimeout)
	defer cancel()
	sem := make(chan struct{}, workspaceGitConcurrency)
	results := make(chan workspaceGitInspection, len(checkouts))
	var wg sync.WaitGroup
	for _, checkout := range checkouts {
		wg.Add(1)
		go func(checkout gitStatusCheckout) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results <- workspaceGitInspection{checkout: checkout, err: ctx.Err()}
				return
			}
			gitCtx, identityErr := withGitCheckoutIdentity(ctx, checkout.path, nil)
			if identityErr != nil {
				results <- workspaceGitInspection{checkout: checkout, err: identityErr}
				return
			}
			status, err := s.fileOps.GitStatusPorcelain(gitCtx, checkout.path)
			item := workspaceGitInspection{checkout: checkout, status: status, err: err}
			if err == nil && includeBranch {
				item.branch, item.branchErr = s.fileOps.GitCurrentBranch(gitCtx, checkout.path)
			}
			results <- item
		}(checkout)
	}
	wg.Wait()
	close(results)
	out := make([]workspaceGitInspection, 0, len(checkouts))
	for item := range results {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].checkout.path < out[j].checkout.path })
	return out
}

func checkoutError(checkout gitStatusCheckout, err error) FileCheckoutError {
	return FileCheckoutError{Kind: checkout.kind, Agent: checkout.agent, Repo: checkout.repo, Error: err.Error()}
}

func sortCheckoutErrors(errors []FileCheckoutError) {
	sort.Slice(errors, func(i, j int) bool {
		left := errors[i].Kind + "\x00" + errors[i].Agent + "\x00" + errors[i].Repo
		right := errors[j].Kind + "\x00" + errors[j].Agent + "\x00" + errors[j].Repo
		return left < right
	})
}

func (s *fileServiceImpl) RepairCheckout(ctx context.Context, wsID string, req FileCheckoutRepairRequest) (*RepairResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, newTimeout("checkout repair canceled")
	}
	scope := strings.TrimSpace(req.Scope)
	target := strings.TrimSpace(req.Target)
	repo := strings.TrimSpace(req.Repo)
	if scope == "" {
		return nil, newInvalid("scope is required")
	}
	if target == "" {
		return nil, newInvalid("target is required")
	}
	result, err := s.fileOps.RepairCheckout(wsID, scope, target, repo, req.Force)
	if err != nil {
		if errors.Is(err, ErrCheckoutTargetNotAllowed) || errors.Is(err, ErrAgentRepoNotAllowed) {
			return nil, newInvalid(err.Error())
		}
		return nil, newInternal("failed to repair checkout", err)
	}
	return &result, nil
}

func repoCheckoutPath(wsRoot string, repo WorkspaceRepo) string {
	if repo.Path != "" {
		if filepath.IsAbs(repo.Path) {
			return repo.Path
		}
		return filepath.Join(wsRoot, repo.Path)
	}
	return filepath.Join(wsRoot, repo.Name)
}

func agentCheckoutRepos(repos []WorkspaceRepo, agent WorkspaceAgent) []WorkspaceRepo {
	if len(agent.Repos) == 0 && len(agent.RepoGroups) == 0 {
		return append([]WorkspaceRepo(nil), repos...)
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
	out := make([]WorkspaceRepo, 0, len(allowed))
	for _, repo := range repos {
		if allowed[repo.Name] {
			out = append(out, repo)
		}
	}
	return out
}

func mergePrefixedGitStatus(access fileAccess, dst map[string]string, prefix string, status map[string]string) {
	for path, xy := range status {
		if prefix != "" {
			path = prefix + "/" + path
		}
		if !filePathAllowsSensitive(access, path) {
			continue
		}
		dst[path] = xy
	}
}

func filterSensitiveGitStatus(access fileAccess, status map[string]string) map[string]string {
	if access.sensitive {
		return status
	}
	filtered := make(map[string]string, len(status))
	for path, xy := range status {
		if filePathAllowsSensitive(access, path) {
			filtered[path] = xy
		}
	}
	return filtered
}

func validateGitCheckoutRoot(root string) error {
	fi, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return newNotFound("git checkout not found")
		}
		return newInternal("failed to stat git checkout", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return newForbidden("refusing to follow symlink")
	}
	if !fi.IsDir() {
		return newInvalid("git checkout root is not a directory")
	}
	gitInfo, err := os.Lstat(filepath.Join(root, ".git"))
	if err != nil {
		if os.IsNotExist(err) {
			return newNotFound("git checkout not found")
		}
		return newInternal("failed to stat git metadata", err)
	}
	if gitInfo.Mode()&os.ModeSymlink != 0 {
		return newForbidden("refusing to follow symlink")
	}
	return nil
}

func withGitCheckoutIdentity(ctx context.Context, checkoutRoot string, heldRoot *scopedRoot) (context.Context, error) {
	absRoot, err := filepath.Abs(checkoutRoot)
	if err != nil {
		return nil, newInternal("failed to resolve git checkout", err)
	}
	if heldRoot != nil && heldRoot.store != nil && heldRoot.store.root != nil && filepath.Clean(heldRoot.path) == filepath.Clean(absRoot) {
		info, err := heldRoot.store.root.Stat(".")
		if err != nil {
			return nil, newInternal("failed to stat held git checkout", err)
		}
		if !info.IsDir() {
			return nil, newInvalid("git checkout root is not a directory")
		}
		return WithGitWorktreeIdentity(ctx, absRoot, info), nil
	}
	// Workspace fanout and workspace-scoped nested checkouts do not hold a
	// checkout-specific root descriptor, so retain the best-effort path capture.
	info, err := os.Lstat(absRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, newNotFound("git checkout not found")
		}
		return nil, newInternal("failed to stat git checkout", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, newForbidden("refusing to follow symlink")
	}
	if !info.IsDir() {
		return nil, newInvalid("git checkout root is not a directory")
	}
	return WithGitWorktreeIdentity(ctx, absRoot, info), nil
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
	if err := validatePathWithinDir(absCheckout, absRoot); err != nil {
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

func checkoutPathPresent(wsRoot, checkoutPath string) bool {
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
	if err := validatePathWithinDir(absCheckout, absRoot); err != nil {
		return false
	}
	if err := validateNoSymlinkComponents(absRoot, absCheckout); err != nil {
		return false
	}
	fi, err := os.Lstat(absCheckout)
	if err != nil {
		return false
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return false
	}
	return true
}
