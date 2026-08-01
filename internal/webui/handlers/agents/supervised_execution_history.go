package agents

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
)

// AgentTaskRunHistoryReader is the exact Execution read projection required
// by the supervised-agent history endpoint. Composition adapts the remaining
// persistence reader; the HTTP projection has no Store dependency.
type AgentTaskRunHistoryReader func(
	context.Context,
	string,
	string,
) ([]*domain.TaskRun, error)

// supervisedExecutionHistory projects canonical Execution TaskRuns and
// Interaction-owned AgentSessions into the one history shape consumed by the
// supervised-agent UI. TaskRun is authoritative for batch execution; the
// AgentSession half is retained for interactive history and pre-Phase-5 data.
//
//nolint:funlen // Keep canonical TaskRun projection, legacy-session deduplication, and stable ordering as one deterministic compatibility merge.
func (m *Module) supervisedExecutionHistory(
	ctx context.Context,
	workspaceKey, agentID string,
	limit int,
) ([]*agentHistorySessionDTO, error) {
	if m == nil || m.taskRunHistory == nil {
		return nil, agentsmodule.ErrUnavailable
	}
	taskRuns, err := m.taskRunHistory(ctx, workspaceKey, agentID)
	if err != nil {
		return nil, err
	}
	sessions, err := m.listAgentSessionsForHistory(ctx, workspaceKey, agentID, 0)
	if err != nil {
		return nil, err
	}

	history := make([]*agentHistorySessionDTO, 0, len(taskRuns)+len(sessions))
	representedTaskRuns := make(map[string]struct{}, len(taskRuns))
	representedSessionIDs := make(map[string]struct{}, len(taskRuns))
	for _, run := range taskRuns {
		if run == nil ||
			strings.TrimSpace(run.WorkspaceKey) != workspaceKey ||
			strings.TrimSpace(run.WorkerProfileID) != agentID {
			continue
		}
		item := newTaskRunHistorySessionDTO(run)
		if item == nil {
			continue
		}
		history = append(history, item)
		representedTaskRuns[strings.TrimSpace(run.TaskRunID)] = struct{}{}
		representedSessionIDs[item.SessionID] = struct{}{}
	}
	for _, session := range sessions {
		if session == nil ||
			strings.TrimSpace(session.WorkspaceKey) != workspaceKey ||
			strings.TrimSpace(session.AgentID) != agentID {
			continue
		}
		if _, represented := representedTaskRuns[legacyHistoryTaskRunID(session)]; represented {
			continue
		}
		if _, represented := representedSessionIDs[strings.TrimSpace(session.SessionID)]; represented {
			continue
		}
		if item := newAgentHistorySessionDTO(session); item != nil {
			history = append(history, item)
			representedSessionIDs[item.SessionID] = struct{}{}
		}
	}
	history, err = m.appendLocalSessionHistory(
		ctx,
		workspaceKey,
		agentID,
		history,
		representedSessionIDs,
	)
	if err != nil {
		return nil, err
	}

	return sortAndLimitAgentHistory(history, limit), nil
}

func (m *Module) appendLocalSessionHistory(
	ctx context.Context,
	workspaceKey, agentID string,
	history []*agentHistorySessionDTO,
	representedSessionIDs map[string]struct{},
) ([]*agentHistorySessionDTO, error) {
	if m.localSessionHistory == nil {
		return history, nil
	}
	localSessions, err := m.localSessionHistory.ListAgentLocalSessions(ctx, workspaceKey, agentID)
	if err != nil {
		return nil, err
	}
	for i := range localSessions {
		item := newLocalHistorySessionDTO(localSessions[i], workspaceKey, agentID)
		if item == nil {
			continue
		}
		if _, represented := representedSessionIDs[item.SessionID]; represented {
			continue
		}
		representedSessionIDs[item.SessionID] = struct{}{}
		history = append(history, item)
	}
	return history, nil
}

func newLocalHistorySessionDTO(
	item agentLocalSessionHistoryItem,
	workspaceKey, agentID string,
) *agentHistorySessionDTO {
	if strings.TrimSpace(item.SessionID) == "" ||
		strings.TrimSpace(item.AgentName) != agentID {
		return nil
	}
	startedAt := nonZeroAgentSessionTime(item.StartedAt)
	updatedAt := item.StartedAt
	if item.EndedAt != nil && !item.EndedAt.IsZero() {
		updatedAt = *item.EndedAt
	}
	var exitCode *int
	if string(item.Status) != "running" {
		value := item.ExitCode
		exitCode = &value
	}
	metadata := publicAgentSessionMetadata(map[string]string{
		"backend":           item.Backend,
		"runtime_strategy":  item.RuntimeStrategy,
		"delivery":          item.DeliveryMode,
		"patch_back_status": item.PatchBackStatus,
		"local_branch":      item.LocalBranch,
		"head_sha":          item.HeadSHA,
		"github_branch":     item.GitHubBranch,
		"github_pr_url":     item.GitHubPRURL,
	})
	return &agentHistorySessionDTO{
		WorkspaceKey: workspaceKey,
		SessionID:    item.SessionID,
		AgentID:      agentID,
		Kind:         domain.AgentSessionKindTask,
		TaskID:       item.TaskID,
		Status:       agentHistoryStatusFromLocalSession(string(item.Status)),
		Phase:        item.Phase,
		Attempt:      item.AttemptNum,
		StartedAt:    startedAt,
		FinishedAt:   cloneNonZeroAgentSessionTime(item.EndedAt),
		ErrorClass:   item.ErrorClass,
		ExitCode:     exitCode,
		Metadata:     metadata,
		CreatedAt:    item.StartedAt,
		UpdatedAt:    updatedAt,
	}
}

func agentHistoryStatusFromLocalSession(status string) domain.AgentSessionStatus {
	switch status {
	case "completed":
		return domain.AgentSessionCompleted
	case "failed":
		return domain.AgentSessionFailed
	case "aborted":
		return domain.AgentSessionCancelled
	default:
		return domain.AgentSessionRunning
	}
}

func sortAndLimitAgentHistory(
	history []*agentHistorySessionDTO,
	limit int,
) []*agentHistorySessionDTO {
	sort.SliceStable(history, func(i, j int) bool {
		left, right := agentHistoryTime(history[i]), agentHistoryTime(history[j])
		if !left.Equal(right) {
			return left.After(right)
		}
		return history[i].SessionID > history[j].SessionID
	})
	if limit > 0 && len(history) > limit {
		history = history[:limit]
	}
	return history
}

func newTaskRunHistorySessionDTO(run *domain.TaskRun) *agentHistorySessionDTO {
	if run == nil {
		return nil
	}
	startedAt := nonZeroAgentSessionTime(run.StartedAt)
	return &agentHistorySessionDTO{
		WorkspaceKey:  run.WorkspaceKey,
		SessionID:     domain.PublicTaskRunSessionID(run),
		AgentID:       run.WorkerProfileID,
		NodeID:        run.NodeID,
		Kind:          domain.AgentSessionKindTask,
		TaskID:        run.TaskID,
		Status:        agentHistoryStatusFromTaskRun(run.Status),
		StartedAt:     startedAt,
		LastHeartbeat: nonZeroAgentSessionTime(run.LastHeartbeat),
		FinishedAt:    cloneNonZeroAgentSessionTime(run.FinishedAt),
		ErrorClass:    run.ErrorClass,
		ExitCode:      run.ExitCode,
		Metadata:      publicAgentSessionMetadata(run.RuntimeMetadata),
		CreatedAt:     run.CreatedAt,
		UpdatedAt:     run.UpdatedAt,
	}
}

func agentHistoryStatusFromTaskRun(status domain.TaskRunStatus) domain.AgentSessionStatus {
	switch status {
	case domain.TaskRunQueued:
		return domain.AgentSessionQueued
	case domain.TaskRunRunning:
		return domain.AgentSessionRunning
	case domain.TaskRunCompleted:
		return domain.AgentSessionCompleted
	case domain.TaskRunFailed:
		return domain.AgentSessionFailed
	case domain.TaskRunCancelled:
		return domain.AgentSessionCancelled
	default:
		return domain.AgentSessionFailed
	}
}

func legacyHistoryTaskRunID(session *domain.AgentSession) string {
	if session == nil || session.Metadata == nil {
		return ""
	}
	return strings.TrimSpace(session.Metadata["task_run_id"])
}

func agentHistoryTime(item *agentHistorySessionDTO) time.Time {
	if item == nil {
		return time.Time{}
	}
	if item.StartedAt != nil && !item.StartedAt.IsZero() {
		return *item.StartedAt
	}
	return item.CreatedAt
}
