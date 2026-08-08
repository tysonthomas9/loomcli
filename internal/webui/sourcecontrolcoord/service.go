package sourcecontrolcoord

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/pathsec"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/servercapabilities"
)

// Compile-time check.
var _ DiffService = (*diffServiceImpl)(nil)

var validGitRef = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_./-]*$`)

// diffServiceImpl is the concrete implementation of DiffService.
type diffServiceImpl struct {
	gitOps    ops.GitOps
	backendFn IssueBackendProvider
	scope     WorkspaceScope
}

type IssueBackendProvider func(context.Context) servercapabilities.IssueBackend
type WorkspaceScope func(context.Context, string) context.Context

// NewDiffService creates a new DiffService implementation.
func NewDiffService(gitOps ops.GitOps, backendFn IssueBackendProvider, scope WorkspaceScope) DiffService {
	return &diffServiceImpl{gitOps: gitOps, backendFn: backendFn, scope: scope}
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
	if !pathsec.ValidateDiffPath(filePath) {
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
	if s.backendFn == nil {
		return nil, apperrors.ErrUnavailable("issue backend unavailable")
	}
	if s.scope == nil {
		return nil, apperrors.ErrUnavailable("workspace scope unavailable")
	}
	ctx = s.scope(ctx, wsID)
	be := s.backendFn(ctx)
	if be == nil {
		return nil, apperrors.ErrUnavailable("issue backend unavailable")
	}
	backendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	issue, err := be.Get(backendCtx, issueID)
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
