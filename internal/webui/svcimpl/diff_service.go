package svcimpl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Compile-time check.
var _ service.DiffService = (*diffServiceImpl)(nil)

// diffServiceImpl is the concrete implementation of DiffService.
type diffServiceImpl struct {
	gitOps ops.GitOps
	pool   daemon.Pool
}

// NewDiffService creates a new DiffService implementation.
func NewDiffService(gitOps ops.GitOps, pool daemon.Pool) service.DiffService {
	return &diffServiceImpl{gitOps: gitOps, pool: pool}
}

func (s *diffServiceImpl) resolveAgent(wsID, agentName string) (*ops.AgentWorktree, error) {
	if err := validateAgentName(agentName); err != nil {
		return nil, err
	}
	wt, err := s.gitOps.ResolveAgentWorktree(wsID, agentName)
	if err != nil {
		return nil, service.ErrNotFound(fmt.Sprintf("agent worktree %q not found", agentName))
	}
	return wt, nil
}

func (s *diffServiceImpl) validateRef(ref string) error {
	if !validGitRef.MatchString(ref) || strings.Contains(ref, "..") {
		return service.ErrValidation("invalid git ref")
	}
	return nil
}

func (s *diffServiceImpl) DiffCommits(_ context.Context, wsID, agentName, from string, limit int) ([]ops.DiffCommitResult, error) {
	wt, err := s.resolveAgent(wsID, agentName)
	if err != nil {
		return nil, err
	}

	if from == "" {
		mergeBase, mbErr := s.gitOps.ResolveMergeBase(wt.Path, wt.DefaultBranch)
		if mbErr != nil {
			return nil, service.ErrInternal("failed to resolve merge-base", mbErr)
		}
		from = mergeBase
	} else {
		if err := s.validateRef(from); err != nil {
			return nil, err
		}
	}

	commits, err := s.gitOps.DiffCommits(wt.Path, from, limit)
	if err != nil {
		return nil, service.ErrInternal("failed to get diff commits", err)
	}
	if commits == nil {
		commits = []ops.DiffCommitResult{}
	}
	markPushedCommits(commits, s.gitOps, wt.Path, wt.DefaultBranch)
	return commits, nil
}

// markPushedCommits annotates each commit with its pushed status using the
// ahead count against origin/<targetBranch>. Commits come back from git log
// in --topo-order (descendants before ancestors), so the first N commits
// (where N = unpushed count) are the unpushed prefix.
// On error, all commits are left with Pushed=false (safe default — no broken links).
func markPushedCommits(commits []ops.DiffCommitResult, gitOps ops.GitOps, worktreePath, targetBranch string) {
	if len(commits) == 0 {
		return
	}
	unpushed, err := gitOps.UnpushedCount(worktreePath, targetBranch)
	if err != nil || unpushed < 0 {
		return
	}
	if unpushed > len(commits) {
		unpushed = len(commits)
	}
	for i := unpushed; i < len(commits); i++ {
		commits[i].Pushed = true
	}
}

func (s *diffServiceImpl) DiffFiles(_ context.Context, wsID, agentName, from, to string) ([]ops.DiffFileResult, error) {
	wt, err := s.resolveAgent(wsID, agentName)
	if err != nil {
		return nil, err
	}

	if to == "" {
		return nil, service.ErrValidation("missing required parameter: to")
	}
	if err := s.validateRef(to); err != nil {
		return nil, err
	}

	if from == "" {
		mergeBase, mbErr := s.gitOps.ResolveMergeBase(wt.Path, wt.DefaultBranch)
		if mbErr != nil {
			return nil, service.ErrInternal("failed to resolve merge-base", mbErr)
		}
		from = mergeBase
	} else {
		if err := s.validateRef(from); err != nil {
			return nil, err
		}
	}

	files, err := s.gitOps.DiffFiles(wt.Path, from, to)
	if err != nil {
		return nil, service.ErrInternal("failed to get diff files", err)
	}
	if files == nil {
		files = []ops.DiffFileResult{}
	}
	return files, nil
}

func (s *diffServiceImpl) DiffFilePatch(_ context.Context, wsID, agentName, from, to, filePath string) (*ops.DiffFilePatchResult, error) {
	wt, err := s.resolveAgent(wsID, agentName)
	if err != nil {
		return nil, err
	}

	if filePath == "" {
		return nil, service.ErrValidation("missing required parameter: path")
	}
	if !validateDiffPath(filePath) {
		return nil, service.ErrValidation("invalid path: must be relative with no '..' traversal")
	}

	if to == "" {
		return nil, service.ErrValidation("missing required parameter: to")
	}
	if err := s.validateRef(to); err != nil {
		return nil, err
	}

	if from == "" {
		mergeBase, mbErr := s.gitOps.ResolveMergeBase(wt.Path, wt.DefaultBranch)
		if mbErr != nil {
			return nil, service.ErrInternal("failed to resolve merge-base", mbErr)
		}
		from = mergeBase
	} else {
		if err := s.validateRef(from); err != nil {
			return nil, err
		}
	}

	result, err := s.gitOps.DiffFilePatch(wt.Path, from, to, filePath)
	if err != nil {
		return nil, service.ErrInternal("failed to get diff patch", err)
	}
	return result, nil
}

func (s *diffServiceImpl) GetIssueDiffStat(ctx context.Context, wsID, issueID string) (*service.IssueDiffStatResult, error) { //nolint:funlen // linear pool+RPC flow with conditional Discard guard
	if issueID == "" {
		return nil, service.ErrValidation("missing issue ID")
	}
	if s.pool == nil {
		return nil, service.ErrUnavailable("daemon not available")
	}

	rpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client, err := s.pool.Get(rpcCtx)
	if err != nil {
		return nil, service.ErrUnavailable("daemon not available")
	}
	rpcOK := false
	defer func() {
		if rpcOK {
			s.pool.Put(client)
		} else {
			s.pool.Discard(client)
		}
	}()

	resp, err := client.Show(&rpc.ShowArgs{ID: issueID})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, service.ErrNotFound(fmt.Sprintf("issue not found: %s", issueID))
		}
		return nil, service.ErrInternal("failed to get issue", err)
	}

	var issue struct {
		Assignee string `json:"assignee"`
	}
	if err := json.Unmarshal(resp.Data, &issue); err != nil {
		return nil, service.ErrInternal("failed to parse issue data", err)
	}
	if issue.Assignee == "" {
		return nil, service.ErrNotFound("issue has no assignee (no agent worktree)")
	}
	rpcOK = true

	wt, err := s.gitOps.ResolveAgentWorktree(wsID, issue.Assignee)
	if err != nil {
		return nil, service.ErrNotFound(fmt.Sprintf("agent worktree not found for %s", issue.Assignee))
	}

	stats := s.gitOps.DiffStat(wt.Path, wt.DefaultBranch)
	return &service.IssueDiffStatResult{
		Branch:  wt.Branch,
		Added:   stats.LinesAdded,
		Removed: stats.LinesRemoved,
	}, nil
}
