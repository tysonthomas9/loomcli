package readprojection

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

var (
	ErrInvalidIssueDiffQuery = errors.New("issue diff: invalid query")
	ErrIssueDiffNotFound     = errors.New("issue diff: not found")
	ErrIssueDiffUnavailable  = errors.New("issue diff: unavailable")
)

// IssueDiffWorkItemQuery is the immutable Work Items view required by the
// cross-capability issue-diff projection.
type IssueDiffWorkItemQuery interface {
	Get(context.Context, workitems.GetQuery) (*workitems.IssueDetail, error)
}

// IssueDiffWorkItemProvider resolves the Work Items owner for one exact
// Workspace without exposing its aggregate or persistence adapter.
type IssueDiffWorkItemProvider func(context.Context, string) IssueDiffWorkItemQuery

// IssueDiffSourceControlBrowse is the narrow Source Control view consumed by
// the projection.
type IssueDiffSourceControlBrowse interface {
	DiffStat(context.Context, sourcecontrol.AgentQuery) (sourcecontrol.AgentDiffStat, error)
}

// IssueDiffProjection joins immutable Work Items assignment state to Source
// Control change statistics. It owns and mutates neither capability.
type IssueDiffProjection interface {
	GetIssueDiff(context.Context, IssueDiffQuery) (IssueDiffResult, error)
}

type IssueDiffQuery struct {
	WorkspaceKey string
	IssueID      string
}

type IssueDiffResult struct {
	Branch  string
	Added   int
	Removed int
}

type issueDiffProjection struct {
	workItems IssueDiffWorkItemProvider
	browse    IssueDiffSourceControlBrowse
}

var _ IssueDiffProjection = (*issueDiffProjection)(nil)

func NewIssueDiffProjection(
	workItems IssueDiffWorkItemProvider,
	browse IssueDiffSourceControlBrowse,
) (IssueDiffProjection, error) {
	if workItems == nil || browse == nil {
		return nil, fmt.Errorf(
			"compose Issue Diff projection: Work Items and Source Control are required: %w",
			ErrIssueDiffUnavailable,
		)
	}
	return &issueDiffProjection{workItems: workItems, browse: browse}, nil
}

func (projection *issueDiffProjection) GetIssueDiff(
	ctx context.Context,
	query IssueDiffQuery,
) (IssueDiffResult, error) {
	query.WorkspaceKey = strings.TrimSpace(query.WorkspaceKey)
	query.IssueID = strings.TrimSpace(query.IssueID)
	if ctx == nil || query.WorkspaceKey == "" || query.IssueID == "" {
		return IssueDiffResult{}, ErrInvalidIssueDiffQuery
	}
	items := projection.workItems(ctx, query.WorkspaceKey)
	if items == nil {
		return IssueDiffResult{}, ErrIssueDiffUnavailable
	}
	issue, err := items.Get(ctx, workitems.GetQuery{IssueID: query.IssueID})
	if err != nil {
		if errors.Is(err, workitems.ErrNotFound) {
			return IssueDiffResult{}, ErrIssueDiffNotFound
		}
		return IssueDiffResult{}, fmt.Errorf("read Work Item: %w", err)
	}
	if issue == nil || strings.TrimSpace(issue.Assignee) == "" {
		return IssueDiffResult{}, ErrIssueDiffNotFound
	}
	stat, err := projection.browse.DiffStat(ctx, sourcecontrol.AgentQuery{
		WorkspaceKey: query.WorkspaceKey,
		AgentID:      strings.TrimSpace(issue.Assignee),
	})
	if err != nil {
		if errors.Is(err, sourcecontrol.ErrNotFound) {
			return IssueDiffResult{}, ErrIssueDiffNotFound
		}
		if errors.Is(err, sourcecontrol.ErrUnavailable) {
			return IssueDiffResult{}, ErrIssueDiffUnavailable
		}
		return IssueDiffResult{}, fmt.Errorf("read Source Control diff stat: %w", err)
	}
	return IssueDiffResult{
		Branch: stat.Branch, Added: stat.LinesAdded, Removed: stat.LinesRemoved,
	}, nil
}

func IssueDiffPublicErrorMessage(err error) string {
	switch {
	case errors.Is(err, ErrInvalidIssueDiffQuery):
		return "invalid issue diff query"
	case errors.Is(err, ErrIssueDiffNotFound):
		return "issue diff not found"
	case errors.Is(err, ErrIssueDiffUnavailable):
		return "issue diff unavailable"
	default:
		return "issue diff failed"
	}
}
