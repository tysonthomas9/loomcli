package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
	"github.com/tysonthomas9/loomcli/internal/webui/worktreegroups"
)

const (
	defaultWorktreeRepoTimeout = 60 * time.Second

	worktreeStatusCreated    = "created"
	worktreeStatusExists     = "exists"
	worktreeStatusReused     = "reused"
	worktreeStatusConflict   = "conflict"
	worktreeStatusError      = "error"
	worktreeStatusRolledBack = "rolled_back"
)

type worktreeGroupStore interface {
	List(ctx context.Context, workspaceID string) ([]worktreegroups.TerminalWorktreeGroup, error)
	WithWorkspaceLock(workspaceID string, fn func(worktreeGroupLockedStore) error) error
}

type worktreeGroupLockedStore interface {
	Get(ctx context.Context, name string) (*worktreegroups.TerminalWorktreeGroup, error)
	Add(ctx context.Context, group worktreegroups.TerminalWorktreeGroup) error
}

type realWorktreeGroupStore struct {
	store *worktreegroups.Store
}

func (s realWorktreeGroupStore) List(ctx context.Context, workspaceID string) ([]worktreegroups.TerminalWorktreeGroup, error) {
	return s.store.List(ctx, workspaceID)
}

func (s realWorktreeGroupStore) WithWorkspaceLock(workspaceID string, fn func(worktreeGroupLockedStore) error) error {
	return s.store.WithWorkspaceLock(workspaceID, func(locked *worktreegroups.LockedWorkspace) error {
		return fn(locked)
	})
}

// WorktreeGroupService creates and lists terminal worktree groups.
type WorktreeGroupService struct {
	workspaceStore store.Store
	groupStore     worktreeGroupStore
	repoTimeout    time.Duration
	newID          func() string
}

// NewWorktreeGroupService creates a terminal worktree group service. It returns
// nil when required persistence dependencies are unavailable.
func NewWorktreeGroupService(workspaceStore store.Store, groupStore *worktreegroups.Store) *WorktreeGroupService {
	if workspaceStore == nil || groupStore == nil {
		return nil
	}
	return &WorktreeGroupService{
		workspaceStore: workspaceStore,
		groupStore:     realWorktreeGroupStore{store: groupStore},
		repoTimeout:    defaultWorktreeRepoTimeout,
		newID:          uuid.NewString,
	}
}

// CreateWorktreeGroupRequest is the POST body for creating a terminal worktree
// group.
type CreateWorktreeGroupRequest struct {
	Name  string   `json:"name"`
	Repos []string `json:"repos,omitempty"`
	Base  string   `json:"base,omitempty"`
}

// WorktreeGroupResult reports the outcome for one requested repository.
type WorktreeGroupResult struct {
	Repo    string `json:"repo"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// CreateWorktreeGroupResponse is returned for successful creates and carries
// per-repo results for failed creates.
type CreateWorktreeGroupResponse struct {
	Group   *worktreegroups.TerminalWorktreeGroup `json:"group,omitempty"`
	Results []WorktreeGroupResult                 `json:"results"`
}

type localWorktreeRepo struct {
	name string
	path string
}

type repoCreateAttempt struct {
	repo            localWorktreeRepo
	target          string
	resultIndex     int
	createdWorktree bool
	createdBranch   bool
	member          worktreegroups.WorktreeGroupMember
}

// List returns user-created terminal worktree groups for a workspace.
func (s *WorktreeGroupService) List(ctx context.Context, workspaceID string) ([]worktreegroups.TerminalWorktreeGroup, error) {
	if s == nil || s.groupStore == nil {
		return nil, service.ErrUnavailable("terminal worktree groups are unavailable")
	}
	groups, err := s.groupStore.List(ctx, workspaceID)
	if err != nil {
		return nil, service.ErrInternal("list terminal worktree groups", err)
	}
	return groups, nil
}

// Create creates a terminal worktree group using all-or-nothing semantics.
func (s *WorktreeGroupService) Create(ctx context.Context, workspaceID string, req CreateWorktreeGroupRequest) (*CreateWorktreeGroupResponse, error) {
	if s == nil || s.workspaceStore == nil || s.groupStore == nil {
		return nil, service.ErrUnavailable("terminal worktree groups are unavailable")
	}
	if err := localworkspace.ValidateWorktreeName(req.Name); err != nil {
		return &CreateWorktreeGroupResponse{Results: []WorktreeGroupResult{}}, service.ErrValidation(err.Error())
	}

	var resp *CreateWorktreeGroupResponse
	err := s.groupStore.WithWorkspaceLock(workspaceID, func(locked worktreeGroupLockedStore) error {
		existing, err := locked.Get(ctx, req.Name)
		if err != nil {
			return service.ErrInternal("get terminal worktree group", err)
		}
		if existing != nil {
			return service.ErrConflict(fmt.Sprintf("worktree group '%s' already exists", req.Name))
		}
		var createErr error
		resp, createErr = s.createLocked(ctx, workspaceID, req, locked)
		return createErr
	})
	return resp, err
}

func (s *WorktreeGroupService) createLocked(ctx context.Context, workspaceID string, req CreateWorktreeGroupRequest, locked worktreeGroupLockedStore) (*CreateWorktreeGroupResponse, error) {
	wsData, err := storeadapter.BuildWorkspaceDataForKey(ctx, s.workspaceStore, workspaceID)
	if err != nil {
		return &CreateWorktreeGroupResponse{Results: []WorktreeGroupResult{}}, service.ErrNotFound(fmt.Sprintf("workspace %q not found", workspaceID))
	}
	root, err := localworkspace.TerminalGroupRootPath(wsData.Path, req.Name)
	if err != nil {
		return &CreateWorktreeGroupResponse{Results: []WorktreeGroupResult{}}, service.ErrValidation(err.Error())
	}

	selected, results := selectWorktreeRepos(wsData.Repos, req.Repos)
	if len(selected) == 0 {
		if len(results) == 0 {
			return &CreateWorktreeGroupResponse{Results: results}, service.ErrValidation("no local repositories are available")
		}
		return &CreateWorktreeGroupResponse{Results: results}, service.ErrValidation("no requested repositories are local")
	}

	attempts, members, results := s.createRepoAttempts(ctx, root, selected, req, results)

	if hasFailedWorktreeResult(results) {
		rollbackWorktreeCreate(root, req.Name, attempts, results)
		return &CreateWorktreeGroupResponse{Results: results}, aggregateWorktreeFailure(results)
	}

	group := worktreegroups.TerminalWorktreeGroup{
		ID:        s.newID(),
		Name:      req.Name,
		Root:      root,
		Members:   members,
		CreatedAt: time.Now().UTC(),
	}
	if err := locked.Add(ctx, group); err != nil {
		rollbackWorktreeCreate(root, req.Name, attempts, results)
		return &CreateWorktreeGroupResponse{Results: results}, service.ErrInternal("persist terminal worktree group", err)
	}
	return &CreateWorktreeGroupResponse{Group: &group, Results: results}, nil
}

func (s *WorktreeGroupService) createRepoAttempts(ctx context.Context, root string, selected []localWorktreeRepo, req CreateWorktreeGroupRequest, results []WorktreeGroupResult) ([]repoCreateAttempt, []worktreegroups.WorktreeGroupMember, []WorktreeGroupResult) {
	attempts := make([]repoCreateAttempt, 0, len(selected))
	members := make([]worktreegroups.WorktreeGroupMember, 0, len(selected))
	for _, repo := range selected {
		target := filepath.Join(root, repo.name)
		resultIndex := len(results)
		results = append(results, WorktreeGroupResult{Repo: repo.name})
		attempt := repoCreateAttempt{repo: repo, target: target, resultIndex: resultIndex}

		repoCtx, cancel := context.WithTimeout(ctx, s.repoTimeout)
		member, status, msg, createdWorktree, createdBranch := createRepoWorktree(repoCtx, repo, target, req.Name, req.Base)
		cancel()

		results[resultIndex].Status = status
		results[resultIndex].Message = msg
		attempt.createdWorktree = createdWorktree
		attempt.createdBranch = createdBranch
		attempt.member = member
		attempts = append(attempts, attempt)
		if isSuccessfulWorktreeStatus(status) {
			members = append(members, member)
		}
	}
	return attempts, members, results
}

func selectWorktreeRepos(repos []ops.WorkspaceRepo, requested []string) ([]localWorktreeRepo, []WorktreeGroupResult) {
	candidates := make(map[string]localWorktreeRepo, len(repos))
	allLocal := make([]localWorktreeRepo, 0, len(repos))
	for _, repo := range repos {
		if repo.Path == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(repo.Path, ".git")); err != nil {
			continue
		}
		if localworkspace.IsGitLinkedWorktree(repo.Path) {
			continue
		}
		local := localWorktreeRepo{name: repo.Name, path: repo.Path}
		candidates[repo.Name] = local
		allLocal = append(allLocal, local)
	}

	if requested == nil {
		return allLocal, nil
	}

	selected := make([]localWorktreeRepo, 0, len(requested))
	results := make([]WorktreeGroupResult, 0)
	seen := make(map[string]bool, len(requested))
	for _, name := range requested {
		if seen[name] {
			results = append(results, WorktreeGroupResult{
				Repo:    name,
				Status:  worktreeStatusError,
				Message: "repo requested more than once",
			})
			continue
		}
		seen[name] = true
		repo, ok := candidates[name]
		if !ok {
			results = append(results, WorktreeGroupResult{
				Repo:    name,
				Status:  worktreeStatusError,
				Message: "repo is not local",
			})
			continue
		}
		selected = append(selected, repo)
	}
	return selected, results
}

func isSuccessfulWorktreeStatus(status string) bool {
	return status == worktreeStatusCreated || status == worktreeStatusExists || status == worktreeStatusReused
}

func hasFailedWorktreeResult(results []WorktreeGroupResult) bool {
	for _, result := range results {
		if !isSuccessfulWorktreeStatus(result.Status) {
			return true
		}
	}
	return false
}

func aggregateWorktreeFailure(results []WorktreeGroupResult) error {
	for _, result := range results {
		if result.Status == worktreeStatusConflict {
			return service.ErrConflict("failed to create terminal worktree group")
		}
	}
	return service.ErrValidation("failed to create terminal worktree group")
}

func createRepoWorktree(ctx context.Context, repo localWorktreeRepo, target, name, base string) (worktreegroups.WorktreeGroupMember, string, string, bool, bool) {
	member := worktreegroups.WorktreeGroupMember{
		RepoName: repo.name,
		Path:     target,
	}

	if branch, ok, err := existingTargetBranch(ctx, target); ok {
		if err != nil {
			return member, worktreeStatusError, err.Error(), false, false
		}
		if strings.TrimSpace(branch) != name {
			return member, worktreeStatusError, "target worktree is on branch " + strings.TrimSpace(branch), false, false
		}
		return member, worktreeStatusExists, "", false, false
	}

	if occupied, msg := prepareWorktreeTarget(target); occupied {
		return member, worktreeStatusError, msg, false, false
	}

	branchExisted := gitRefExists(ctx, repo.path, "refs/heads/"+name)
	if branchExisted {
		member.ReusedBranch = true
	} else {
		baseBranch, detached, err := resolveBaseRecord(ctx, repo.path, base)
		if err != nil {
			return member, worktreeStatusError, err.Error(), false, false
		}
		member.BaseBranch = baseBranch
		member.BaseDetached = detached
	}

	defaultBranch := strings.TrimSpace(base)
	if defaultBranch != "" && defaultBranch == name {
		return member, worktreeStatusError, "base branch must differ from worktree name", false, false
	}

	if err := localworkspace.EnsureGitWorktreeFromBranchCtx(ctx, repo.path, target, name, "", defaultBranch); err != nil {
		status, msg := classifyWorktreeGitFailure(err)
		return member, status, msg, false, false
	}

	if branchExisted {
		return member, worktreeStatusReused, "", true, false
	}
	return member, worktreeStatusCreated, "", true, true
}

func existingTargetBranch(ctx context.Context, target string) (string, bool, error) {
	if _, err := os.Stat(filepath.Join(target, ".git")); err == nil {
		out, gitErr := runTerminalGit(ctx, target, "rev-parse", "--abbrev-ref", "HEAD")
		if gitErr != nil {
			return "", true, gitErr
		}
		return strings.TrimSpace(out), true, nil
	}
	return "", false, nil
}

func prepareWorktreeTarget(target string) (bool, string) {
	info, err := os.Stat(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, ""
		}
		return true, strings.TrimSpace(err.Error())
	}
	if !info.IsDir() {
		return true, "target path is occupied"
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return true, strings.TrimSpace(err.Error())
	}
	if len(entries) > 0 {
		return true, "target path is occupied"
	}
	if err := os.Remove(target); err != nil {
		return true, strings.TrimSpace(err.Error())
	}
	return false, ""
}

func gitRefExists(ctx context.Context, repoPath, ref string) bool {
	_, err := runTerminalGit(ctx, repoPath, "rev-parse", "--verify", ref)
	return err == nil
}

func resolveBaseRecord(ctx context.Context, repoPath, base string) (string, bool, error) {
	base = strings.TrimSpace(base)
	if base != "" {
		return base, false, nil
	}
	out, err := runTerminalGit(ctx, repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", false, err
	}
	branch := strings.TrimSpace(out)
	if branch != "HEAD" {
		return branch, false, nil
	}
	sha, err := runTerminalGit(ctx, repoPath, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(sha), true, nil
}

func classifyWorktreeGitFailure(err error) (string, string) {
	msg := strings.TrimSpace(err.Error())
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "already used by worktree"),
		strings.Contains(lower, "already checked out"),
		strings.Contains(lower, "already a worktree"):
		return worktreeStatusConflict, msg
	case strings.Contains(lower, "already exists"):
		return worktreeStatusError, "target path occupied"
	case strings.Contains(lower, "fetch base branch"),
		strings.Contains(lower, "resolve base branch"):
		return worktreeStatusError, msg
	default:
		return worktreeStatusError, msg
	}
}

func rollbackWorktreeCreate(root, branchName string, attempts []repoCreateAttempt, results []WorktreeGroupResult) {
	for _, attempt := range attempts {
		if attempt.createdWorktree {
			_, _ = runTerminalGit(context.Background(), attempt.repo.path, "worktree", "remove", "--force", attempt.target)
			results[attempt.resultIndex].Status = worktreeStatusRolledBack
			results[attempt.resultIndex].Message = ""
		}
	}
	for _, attempt := range attempts {
		if attempt.createdBranch {
			_, _ = runTerminalGit(context.Background(), attempt.repo.path, "branch", "-D", branchName)
		}
	}
	removeDirIfEmpty(root)
}

func removeDirIfEmpty(path string) {
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) > 0 {
		return
	}
	_ = os.Remove(path)
}
