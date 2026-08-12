package driverapi

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

const (
	defaultReadyTaskLimit      = 100
	defaultTargetReadyScan     = 1000
	defaultEpicReadyLimit      = 256
	defaultEpicBlockedLimit    = 256
	defaultEpicOpenChildrenMax = 10000
)

func (m *Module) requireWorkItems() (WorkItemOperations, error) {
	if m == nil || m.workItems == nil {
		return nil, fmt.Errorf("driver Work Items capability is not configured: %w", workitems.ErrUnavailable)
	}
	return m.workItems, nil
}

func readyTaskCandidates(
	ctx context.Context,
	items WorkItemOperations,
	epicID,
	issueType,
	sourceRepo string,
	limit int,
) ([]workitems.IssueSummary, error) {
	if limit <= 0 {
		limit = defaultReadyTaskLimit
	}
	values, err := items.Ready(ctx, workitems.AvailabilityQuery{
		ParentID: strings.TrimSpace(epicID), IssueType: strings.TrimSpace(issueType),
		SourceRepos: oneNonEmptyString(sourceRepo), Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list ready tasks: %w", err)
	}
	ready := make([]workitems.IssueSummary, 0, len(values))
	for _, issue := range values {
		if strings.TrimSpace(issue.ID) == "" || strings.EqualFold(strings.TrimSpace(issue.Status), "blocked") {
			continue
		}
		ready = append(ready, issue)
	}
	return ready, nil
}

func readyTaskByID(
	ctx context.Context,
	items WorkItemOperations,
	taskID,
	epicID string,
	limit int,
) (*workitems.IssueSummary, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("task id required: %w", domain.ErrInvalid)
	}
	if limit <= 0 {
		limit = defaultTargetReadyScan
	}
	ready, err := readyTaskCandidates(ctx, items, epicID, "", "", limit)
	if err != nil {
		return nil, err
	}
	for index := range ready {
		if strings.TrimSpace(ready[index].ID) == taskID {
			candidate := ready[index]
			return &candidate, nil
		}
	}
	return nil, fmt.Errorf("task %q is not ready or already claimed: %w", taskID, domain.ErrConflict)
}

func claimedTaskFromWorkItem(
	issue workitems.IssueSummary,
	actor,
	claimActionID string,
	claimedAt time.Time,
) *driverpkg.ClaimedTask {
	if claimedAt.IsZero() {
		claimedAt = time.Now().UTC()
	}
	return &driverpkg.ClaimedTask{
		ID: issue.ID, Title: issue.Title, Status: "in_progress", Priority: issue.Priority,
		IssueType: issue.IssueType, Assignee: issue.Assignee, Labels: append([]string(nil), issue.Labels...),
		SourceRepo: issue.SourceRepo, Parent: issue.Parent, ClaimedBy: actor,
		ClaimedAt: claimedAt, ClaimActionID: claimActionID,
	}
}

func workItemSummaryFromDetail(issue *workitems.IssueDetail) workitems.IssueSummary {
	if issue == nil {
		return workitems.IssueSummary{}
	}
	return workitems.IssueSummary{
		ID: issue.ID, Title: issue.Title, Status: issue.Status, Priority: issue.Priority,
		IssueType: issue.IssueType, Assignee: issue.Assignee, Owner: issue.Owner,
		Labels: append([]string(nil), issue.Labels...), SourceRepo: issue.SourceRepo,
		Repo: issue.Repo, Parent: issue.Parent, Design: issue.Design,
		DesignArtifactID: issue.DesignArtifactID, DesignFormat: issue.DesignFormat,
		HasDesign: issue.HasDesign, Notes: issue.Notes, CreatedBy: issue.CreatedBy,
		CreatedAt: issue.CreatedAt, UpdatedAt: issue.UpdatedAt, ClosedAt: issue.ClosedAt,
		CloseReason: issue.CloseReason, ExternalRef: issue.ExternalRef,
		DueAt: issue.DueAt, DeferUntil: issue.DeferUntil,
		DependencyCount: len(issue.Dependencies), DependentCount: len(issue.Dependents),
	}
}

func loadEpicSnapshot(
	ctx context.Context,
	items WorkItemOperations,
	epicID string,
) (*driverpkg.EpicSnapshot, error) {
	epicID = strings.TrimSpace(epicID)
	if epicID == "" {
		return nil, fmt.Errorf("epic id required: %w", domain.ErrInvalid)
	}
	ready, err := items.Ready(ctx, workitems.AvailabilityQuery{ParentID: epicID, Limit: defaultEpicReadyLimit})
	if err != nil {
		return nil, fmt.Errorf("ready query: %w", err)
	}
	blocked, err := items.Blocked(ctx, workitems.AvailabilityQuery{ParentID: epicID, Limit: defaultEpicBlockedLimit})
	if err != nil {
		return nil, fmt.Errorf("blocked query: %w", err)
	}
	listed, err := items.List(ctx, workitems.ListQuery{Filter: workitems.ListFilter{
		ParentID: epicID,
		Limit:    defaultEpicOpenChildrenMax,
	}})
	if err != nil {
		return nil, fmt.Errorf("list child work: %w", err)
	}
	children := make([]workitems.IssueSummary, 0, len(listed.Issues))
	for _, child := range listed.Issues {
		if child.Status == "closed" || child.Status == "deferred" {
			continue
		}
		children = append(children, child.IssueSummary)
	}
	return &driverpkg.EpicSnapshot{
		EpicID: epicID, ReadyCount: len(ready), BlockedCount: len(blocked), OpenChildrenCount: len(children),
		Ready: epicTaskSummaries(ready), Blocked: epicTaskSummaries(blocked), OpenChildren: epicTaskSummaries(children),
	}, nil
}

func epicTaskSummaries(issues []workitems.IssueSummary) []driverpkg.EpicTaskSummary {
	if len(issues) == 0 {
		return nil
	}
	out := make([]driverpkg.EpicTaskSummary, 0, len(issues))
	for _, issue := range issues {
		out = append(out, driverpkg.EpicTaskSummary{
			ID: issue.ID, Title: issue.Title, Status: issue.Status, Priority: issue.Priority,
			IssueType: issue.IssueType, Assignee: issue.Assignee, Labels: append([]string(nil), issue.Labels...),
			SourceRepo: issue.SourceRepo, Parent: issue.Parent,
			BlockedByCount: issue.BlockedByCount, BlockedBy: append([]string(nil), issue.BlockedBy...),
		})
	}
	return out
}

func oneNonEmptyString(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return []string{value}
}
