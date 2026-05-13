package git

import (
	"context"
	"encoding/json"
	"errors"
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
	if agentName == "" {
		return nil, service.ErrValidation("missing agent name")
	}
	if !service.ValidAgentName.MatchString(agentName) {
		return nil, service.ErrValidation("invalid agent name: must match [a-zA-Z0-9_-]+")
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

func diffBaseError(err error) error {
	if errors.Is(err, ops.ErrDiffBaseNotFound) {
		return service.ErrValidation("failed to resolve diff base: " + err.Error())
	}
	return service.ErrInternal("failed to resolve merge-base", err)
}

func (s *diffServiceImpl) DiffCommits(ctx context.Context, wsID, agentName, from string, limit int) ([]ops.DiffCommitResult, error) {
	wt, err := s.resolveAgent(wsID, agentName)
	if err != nil {
		return nil, err
	}

	if from == "" {
		mergeBase, mbErr := s.gitOps.ResolveMergeBase(wt.Path, wt.DefaultBranch)
		if mbErr != nil {
			return nil, diffBaseError(mbErr)
		}
		from = mergeBase
	} else {
		if err := s.validateRef(from); err != nil {
			return nil, err
		}
	}

	commits, err := s.gitOps.DiffCommits(ctx, wt.Path, from, limit)
	if err != nil {
		return nil, service.ErrInternal("failed to get diff commits", err)
	}
	if commits == nil {
		commits = []ops.DiffCommitResult{}
	}
	return commits, nil
}

func (s *diffServiceImpl) DiffFiles(ctx context.Context, wsID, agentName, from, to string) ([]ops.DiffFileResult, error) {
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
			return nil, diffBaseError(mbErr)
		}
		from = mergeBase
	} else {
		if err := s.validateRef(from); err != nil {
			return nil, err
		}
	}

	files, err := s.gitOps.DiffFiles(ctx, wt.Path, from, to)
	if err != nil {
		return nil, service.ErrInternal("failed to get diff files", err)
	}
	if files == nil {
		files = []ops.DiffFileResult{}
	}
	return files, nil
}

func (s *diffServiceImpl) DiffFilePatch(ctx context.Context, wsID, agentName, from, to, filePath string) (*ops.DiffFilePatchResult, error) {
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
			return nil, diffBaseError(mbErr)
		}
		from = mergeBase
	} else {
		if err := s.validateRef(from); err != nil {
			return nil, err
		}
	}

	result, err := s.gitOps.DiffFilePatch(ctx, wt.Path, from, to, filePath)
	if err != nil {
		return nil, service.ErrInternal("failed to get diff patch", err)
	}
	return result, nil
}

func (s *diffServiceImpl) GetIssueDiffStat(ctx context.Context, wsID, issueID string) (*service.IssueDiffStatResult, error) {
	if issueID == "" {
		return nil, service.ErrValidation("missing issue ID")
	}
	if s.pool == nil {
		return nil, service.ErrUnavailable("issue backend unavailable")
	}

	rpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client, err := s.pool.Get(rpcCtx)
	if err != nil {
		return nil, service.ErrUnavailable("issue backend unavailable")
	}
	defer s.pool.Put(client)

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
