package driver

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	defaultEpicSnapshotReadyLimit   = 256
	defaultEpicSnapshotBlockedLimit = 256
	defaultEpicSnapshotOpenLimit    = 10000
)

type EpicSnapshotOptions struct {
	EpicID       string
	ReadyLimit   int
	BlockedLimit int
	OpenLimit    int
}

type EpicTaskSummary struct {
	ID             string   `json:"id"`
	Title          string   `json:"title,omitempty"`
	Status         string   `json:"status,omitempty"`
	Priority       int      `json:"priority,omitempty"`
	IssueType      string   `json:"issueType,omitempty"`
	Assignee       string   `json:"assignee,omitempty"`
	Labels         []string `json:"labels,omitempty"`
	SourceRepo     string   `json:"sourceRepo,omitempty"`
	Parent         string   `json:"parent,omitempty"`
	BlockedByCount int      `json:"blockedByCount,omitempty"`
	BlockedBy      []string `json:"blockedBy,omitempty"`
}

type EpicSnapshot struct {
	EpicID            string            `json:"epicId"`
	ReadyCount        int               `json:"readyCount"`
	BlockedCount      int               `json:"blockedCount"`
	OpenChildrenCount int               `json:"openChildrenCount"`
	Ready             []EpicTaskSummary `json:"ready,omitempty"`
	Blocked           []EpicTaskSummary `json:"blocked,omitempty"`
	OpenChildren      []EpicTaskSummary `json:"openChildren,omitempty"`
}

type ActiveTaskRunsOptions struct {
	WorkspaceKey string
	DriverRunID  string
	EpicID       string
	Limit        int
}

type ActiveTaskRuns struct {
	WorkspaceKey string                 `json:"workspaceKey,omitempty"`
	DriverRunID  string                 `json:"driverRunId"`
	EpicID       string                 `json:"epicId,omitempty"`
	ActiveCount  int                    `json:"activeCount"`
	TaskRuns     []TaskRunRequestResult `json:"taskRuns,omitempty"`
}

// EpicWorkItems is the exact Work Items projection needed to assemble an epic
// snapshot. The driver owns this consumer port; it does not depend on a
// horizontal repository abstraction.
type EpicWorkItems interface {
	List(context.Context, workitems.ListQuery) (*workitems.ListResult, error)
	Ready(context.Context, workitems.AvailabilityQuery) ([]workitems.IssueSummary, error)
	Blocked(context.Context, workitems.AvailabilityQuery) ([]workitems.IssueSummary, error)
}

func LoadEpicSnapshot(ctx context.Context, items EpicWorkItems, opts EpicSnapshotOptions) (*EpicSnapshot, error) {
	if items == nil {
		return nil, fmt.Errorf("work items projection required: %w", domain.ErrInvalid)
	}
	epicID := strings.TrimSpace(opts.EpicID)
	if epicID == "" {
		return nil, fmt.Errorf("epic id required: %w", domain.ErrInvalid)
	}
	readyLimit := opts.ReadyLimit
	if readyLimit <= 0 {
		readyLimit = defaultEpicSnapshotReadyLimit
	}
	blockedLimit := opts.BlockedLimit
	if blockedLimit <= 0 {
		blockedLimit = defaultEpicSnapshotBlockedLimit
	}
	openLimit := opts.OpenLimit
	if openLimit <= 0 {
		openLimit = defaultEpicSnapshotOpenLimit
	}
	ready, err := items.Ready(ctx, workitems.AvailabilityQuery{ParentID: epicID, Limit: readyLimit})
	if err != nil {
		return nil, fmt.Errorf("ready query: %w", err)
	}
	blocked, err := items.Blocked(ctx, workitems.AvailabilityQuery{ParentID: epicID, Limit: blockedLimit})
	if err != nil {
		return nil, fmt.Errorf("blocked query: %w", err)
	}
	listed, err := items.List(ctx, workitems.ListQuery{Filter: workitems.ListFilter{ParentID: epicID, Limit: openLimit}})
	if err != nil {
		return nil, fmt.Errorf("list child work: %w", err)
	}
	openChildren := make([]workitems.IssueSummary, 0, len(listed.Issues))
	for _, item := range listed.Issues {
		child := item.IssueSummary
		if child.Status == "closed" || child.Status == "deferred" {
			continue
		}
		openChildren = append(openChildren, child)
	}
	return &EpicSnapshot{
		EpicID:            epicID,
		ReadyCount:        len(ready),
		BlockedCount:      len(blocked),
		OpenChildrenCount: len(openChildren),
		Ready:             epicTaskSummaries(ready),
		Blocked:           epicTaskSummaries(blocked),
		OpenChildren:      epicTaskSummaries(openChildren),
	}, nil
}

type taskRunListStore interface {
	TaskRuns() store.TaskRunStore
}

func ListActiveTaskRuns(ctx context.Context, s taskRunListStore, opts ActiveTaskRunsOptions) (*ActiveTaskRuns, error) {
	if s == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	workspaceKey := strings.TrimSpace(opts.WorkspaceKey)
	driverRunID := strings.TrimSpace(opts.DriverRunID)
	if workspaceKey == "" || driverRunID == "" {
		return nil, fmt.Errorf("workspace key and driver run id required: %w", domain.ErrInvalid)
	}
	limit := opts.Limit
	var active []*domain.TaskRun
	for _, status := range []domain.TaskRunStatus{domain.TaskRunQueued, domain.TaskRunRunning} {
		filter := store.TaskRunFilter{DriverRunID: driverRunID, Status: status}
		if limit > 0 {
			filter.Limit = limit
		}
		runs, err := s.TaskRuns().List(ctx, workspaceKey, filter)
		if err != nil {
			return nil, fmt.Errorf("list %s task runs: %w", status, err)
		}
		active = append(active, runs...)
	}
	sort.SliceStable(active, func(i, j int) bool {
		if active[i].CreatedAt.Equal(active[j].CreatedAt) {
			return active[i].TaskRunID < active[j].TaskRunID
		}
		return active[i].CreatedAt.Before(active[j].CreatedAt)
	})
	if limit > 0 && len(active) > limit {
		active = active[:limit]
	}
	results := make([]TaskRunRequestResult, 0, len(active))
	for _, run := range active {
		results = append(results, TaskRunResultFromDomain(run))
	}
	return &ActiveTaskRuns{
		WorkspaceKey: workspaceKey,
		DriverRunID:  driverRunID,
		EpicID:       strings.TrimSpace(opts.EpicID),
		ActiveCount:  len(results),
		TaskRuns:     results,
	}, nil
}

func epicTaskSummaries(issues []workitems.IssueSummary) []EpicTaskSummary {
	if len(issues) == 0 {
		return nil
	}
	out := make([]EpicTaskSummary, 0, len(issues))
	for _, issue := range issues {
		out = append(out, EpicTaskSummary{
			ID:             issue.ID,
			Title:          issue.Title,
			Status:         issue.Status,
			Priority:       issue.Priority,
			IssueType:      issue.IssueType,
			Assignee:       issue.Assignee,
			Labels:         append([]string(nil), issue.Labels...),
			SourceRepo:     issue.SourceRepo,
			Parent:         issue.Parent,
			BlockedByCount: issue.BlockedByCount,
			BlockedBy:      append([]string(nil), issue.BlockedBy...),
		})
	}
	return out
}
