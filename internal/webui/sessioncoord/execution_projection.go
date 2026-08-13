package sessioncoord

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/app/query/runcapture"
	"github.com/tysonthomas9/loomcli/internal/domain"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
)

func (s *sessionServiceImpl) executionTaskRunForSession(
	ctx context.Context,
	wsID, taskID, sessionID string,
) (*domain.TaskRun, error) {
	if s.store == nil || s.store.TaskRuns() == nil {
		return nil, apperrors.ErrNotFound("session not found")
	}
	candidates := []string{sessionID}
	if strings.HasPrefix(sessionID, domain.FlueTaskSessionIDPrefix) {
		if taskRunID := strings.TrimSpace(strings.TrimPrefix(sessionID, domain.FlueTaskSessionIDPrefix)); taskRunID != "" {
			candidates = append(candidates, taskRunID)
		}
	}
	for _, taskRunID := range candidates {
		run, err := s.store.TaskRuns().Get(ctx, wsID, taskRunID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			logger.Error("failed to load execution task run", "workspace_id", wsID, "task_id", taskID, "task_run_id", taskRunID, "err", err)
			return nil, sessionControlPlaneReadError(
				"failed to load session",
				err,
			)
		}
		if run == nil || strings.TrimSpace(run.WorkspaceKey) != strings.TrimSpace(wsID) ||
			strings.TrimSpace(run.TaskRunID) != taskRunID || strings.TrimSpace(run.TaskID) != strings.TrimSpace(taskID) {
			return nil, apperrors.ErrNotFound("session not found")
		}
		return run, nil
	}
	return nil, apperrors.ErrNotFound("session not found")
}

func sessionRecordFromTaskRun(run *domain.TaskRun) SessionRecordView {
	if run == nil {
		return SessionRecordView{}
	}
	startedAt := run.StartedAt
	if startedAt.IsZero() {
		startedAt = run.CreatedAt
	}
	metadata := run.RuntimeMetadata
	diffMeta := decodeDiffStatsMetadata(metadata)
	exitCode := 0
	if run.ExitCode != nil {
		exitCode = *run.ExitCode
	} else if run.Status == domain.TaskRunFailed {
		exitCode = 1
	}
	record := SessionRecordView{
		SchemaVersion:    1,
		SessionID:        domain.PublicTaskRunSessionID(run),
		TaskID:           run.TaskID,
		AgentName:        taskRunAgentName(run),
		Backend:          taskRunBackend(run),
		Model:            strings.TrimSpace(metadata["model"]),
		Phase:            firstNonEmptySessionValue(metadata["phase"], "implementation"),
		StartedAt:        startedAt,
		Status:           sessionStatusFromTaskRun(run.Status),
		AttemptNum:       taskRunAttemptNumber(run),
		ExitCode:         exitCode,
		InputTokens:      run.InputTokens,
		OutputTokens:     run.OutputTokens,
		CacheReadTokens:  run.CacheReadTokens,
		CacheWriteTokens: run.CacheWriteTokens,
		EstimatedCostUSD: run.EstimatedCostUSD,
		ErrorClass:       run.ErrorClass,
		FilesChanged:     diffMeta.FilesChanged,
		LinesAdded:       diffMeta.LinesAdded,
		LinesRemoved:     diffMeta.LinesRemoved,
		FilesTouched:     diffMeta.FilesTouched,
	}
	if run.FinishedAt != nil {
		record.EndedAt = run.FinishedAt
		if !startedAt.IsZero() {
			record.DurationS = run.FinishedAt.Sub(startedAt).Seconds()
		}
	}
	return record
}

// taskRunAgentName reports the product agent that requested a managed TaskRun.
// WorkerProfileID identifies the infrastructure process which claimed the run;
// showing it as the assignee makes every managed task look owned by the shared
// task worker. The immutable policy snapshot is stamped by Loom when the agent
// requests execution, so it is the durable, retry-safe source of attribution.
func taskRunAgentName(run *domain.TaskRun) string {
	if run != nil && len(run.Input) > 0 {
		var input map[string]json.RawMessage
		if err := json.Unmarshal(run.Input, &input); err == nil {
			var policy struct {
				AgentServiceID string `json:"agentServiceId"`
			}
			if err := json.Unmarshal(input["loomAgentPolicy"], &policy); err == nil {
				if agentID := strings.TrimSpace(policy.AgentServiceID); agentID != "" {
					return agentID
				}
			}
		}
	}
	return taskRunWorkerName(run)
}

func taskRunWorkerName(run *domain.TaskRun) string {
	if run == nil {
		return ""
	}
	return firstNonEmptySessionValue(
		run.WorkerProfileID,
		run.RunnerPlacement.RunnerID,
		run.TargetNodeID,
		run.NodeID,
		run.Runner,
		"task-run-worker",
	)
}

func taskRunBackend(run *domain.TaskRun) string {
	if run == nil {
		return ""
	}
	if value := firstNonEmptySessionValue(run.RuntimeMetadata["backend"], run.RuntimeMetadata["runtime"]); value != "" {
		return value
	}
	if strings.TrimSpace(run.RunnerKind) == "flue-workflow" {
		return "flue"
	}
	return strings.TrimSpace(run.Runner)
}

func taskRunAttemptNumber(run *domain.TaskRun) int {
	if run == nil {
		return 0
	}
	attempt := parsePositiveSessionInt(run.RuntimeMetadata["task_run_attempt"])
	schedulerAttempt := parsePositiveSessionInt(run.RuntimeMetadata["scheduler_attempt"])
	if schedulerAttempt > attempt {
		attempt = schedulerAttempt
	}
	if run.Status == domain.TaskRunRunning && schedulerAttempt >= attempt {
		attempt = schedulerAttempt + 1
	}
	return attempt
}

func parsePositiveSessionInt(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 1 {
		return 0
	}
	return value
}

func sessionStatusFromTaskRun(status domain.TaskRunStatus) SessionStatus {
	switch status {
	case domain.TaskRunCompleted:
		return StatusCompleted
	case domain.TaskRunFailed:
		return StatusFailed
	case domain.TaskRunCancelled:
		return StatusAborted
	default:
		return StatusRunning
	}
}

type diffStatsMetadata struct {
	FilesChanged int
	LinesAdded   int
	LinesRemoved int
	FilesTouched []string
}

func decodeDiffStatsMetadata(metadata map[string]string) diffStatsMetadata {
	if metadata == nil {
		return diffStatsMetadata{}
	}
	filesChanged, _ := strconv.Atoi(metadata["files_changed"])
	linesAdded, _ := strconv.Atoi(metadata["lines_added"])
	linesRemoved, _ := strconv.Atoi(metadata["lines_removed"])
	var filesTouched []string
	if value := strings.TrimSpace(metadata["files_touched"]); value != "" {
		filesTouched = strings.Split(value, "\n")
	}
	return diffStatsMetadata{
		FilesChanged: filesChanged, LinesAdded: linesAdded, LinesRemoved: linesRemoved, FilesTouched: filesTouched,
	}
}

func isActiveTaskRun(status domain.TaskRunStatus) bool {
	return status == domain.TaskRunQueued || status == domain.TaskRunRunning
}

func (s *sessionServiceImpl) fillExecutionTaskRunEvidence(
	ctx context.Context,
	item *SessionListItem,
	run *domain.TaskRun,
) {
	if item == nil || run == nil {
		return
	}
	fillExecutionEvidence(item, run.RuntimeMetadata)
	if item.LogsRef == "" {
		item.LogsRef = run.LogsRef
	}
	if s.captures == nil {
		return
	}
	capture, err := s.captures.Get(ctx, runcapture.Query{
		WorkspaceKey: run.WorkspaceKey,
		OwnerKind:    runcapture.OwnerExecution,
		OwnerID:      run.TaskRunID,
		WorkItemID:   run.TaskID,
	})
	if err != nil || capture == nil {
		if err != nil && !errors.Is(err, runcapture.ErrNotFound) {
			item.TranscriptEvidenceStatus = string(runcapture.EvidenceContentUnavailable)
			item.DiffEvidenceStatus = string(runcapture.EvidenceContentUnavailable)
		}
		return
	}
	for _, evidence := range capture.Evidence {
		applySessionEvidence(item, evidence)
	}
}

func (s *sessionServiceImpl) executionTaskRunTranscript(
	ctx context.Context,
	wsID string,
	run *domain.TaskRun,
) ([]artifactsmodule.Event, error) {
	if run == nil {
		return nil, apperrors.ErrNotFound("session not found")
	}
	return s.runCaptureTranscript(ctx, runcapture.Query{
		WorkspaceKey: wsID, OwnerKind: runcapture.OwnerExecution,
		OwnerID: run.TaskRunID, WorkItemID: run.TaskID,
	})
}
