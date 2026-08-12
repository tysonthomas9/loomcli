package sourcecontrolcoord

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
)

// Compile-time check.
var _ DiffService = (*diffServiceImpl)(nil)

var validGitRef = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_./-]*$`)

func validateDiffPath(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") {
		return false
	}
	cleaned := filepath.Clean(path)
	return cleaned != "." && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}

// diffServiceImpl is the concrete implementation of DiffService.
type diffServiceImpl struct {
	gitOps      ops.GitOps
	workItemsFn workitems.Provider
	scope       WorkspaceScope
}

type WorkspaceScope func(context.Context, string) context.Context

// NewDiffService creates a new DiffService implementation.
func NewDiffService(gitOps ops.GitOps, workItemsFn workitems.Provider, scope WorkspaceScope) DiffService {
	return &diffServiceImpl{gitOps: gitOps, workItemsFn: workItemsFn, scope: scope}
}

func (s *diffServiceImpl) resolveAgent(wsID, agentName string) (*ops.AgentWorktree, error) {
	if agentName == "" {
		return nil, apperrors.ErrValidation("missing agent name")
	}
	if !agentcoord.IsValidAgentName(agentName) {
		return nil, apperrors.ErrValidation("invalid agent name")
	}
	wt, err := s.gitOps.ResolveAgentWorktree(wsID, agentName)
	if err != nil {
		return nil, apperrors.ErrNotFound(fmt.Sprintf("agent worktree %q not found", agentName))
	}
	return wt, nil
}

func (s *diffServiceImpl) validateRef(ref string) error {
	if !validGitRef.MatchString(ref) || strings.Contains(ref, "..") {
		return apperrors.ErrValidation("invalid git ref")
	}
	return nil
}

func diffBaseError(err error) error {
	if errors.Is(err, ops.ErrDiffBaseNotFound) {
		return apperrors.ErrValidation("failed to resolve diff base: " + err.Error())
	}
	return apperrors.ErrInternal("failed to resolve merge-base", err)
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
		return nil, apperrors.ErrInternal("failed to get diff commits", err)
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
		return nil, apperrors.ErrValidation("missing required parameter: to")
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
		return nil, apperrors.ErrInternal("failed to get diff files", err)
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
		return nil, apperrors.ErrValidation("missing required parameter: path")
	}
	if !validateDiffPath(filePath) {
		return nil, apperrors.ErrValidation("invalid path: must be relative with no '..' traversal")
	}

	if to == "" {
		return nil, apperrors.ErrValidation("missing required parameter: to")
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
		return nil, apperrors.ErrInternal("failed to get diff patch", err)
	}
	return result, nil
}

func (s *diffServiceImpl) GetIssueDiffStat(ctx context.Context, wsID, issueID string) (*IssueDiffStatResult, error) {
	if issueID == "" {
		return nil, apperrors.ErrValidation("missing issue ID")
	}
	if s.workItemsFn == nil {
		return nil, apperrors.ErrUnavailable("Work Items unavailable")
	}
	if s.scope == nil {
		return nil, apperrors.ErrUnavailable("workspace scope unavailable")
	}
	ctx = s.scope(ctx, wsID)
	items := s.workItemsFn(ctx)
	if items == nil {
		return nil, apperrors.ErrUnavailable("Work Items unavailable")
	}
	backendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	issue, err := items.Get(backendCtx, workitems.GetQuery{IssueID: issueID})
	if err != nil {
		return nil, apperrors.ErrInternal("failed to get issue", err)
	}
	if issue == nil {
		return nil, apperrors.ErrNotFound(fmt.Sprintf("issue not found: %s", issueID))
	}
	if issue.Assignee == "" {
		return nil, apperrors.ErrNotFound("issue has no assignee (no agent worktree)")
	}

	wt, err := s.gitOps.ResolveAgentWorktree(wsID, issue.Assignee)
	if err != nil {
		return nil, apperrors.ErrNotFound(fmt.Sprintf("agent worktree not found for %s", issue.Assignee))
	}

	stats := s.gitOps.DiffStat(wt.Path, wt.DefaultBranch)
	return &IssueDiffStatResult{
		Branch:  wt.Branch,
		Added:   stats.LinesAdded,
		Removed: stats.LinesRemoved,
	}, nil
}
