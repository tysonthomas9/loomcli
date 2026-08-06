package sessioncoord

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
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

func sessionRecordFromTaskRun(run *domain.TaskRun) sessions.SessionRecord {
	if run == nil {
		return sessions.SessionRecord{}
	}
	startedAt := run.StartedAt
	if startedAt.IsZero() {
		startedAt = run.CreatedAt
	}
	metadata := run.RuntimeMetadata
	diffMeta := sessions.DecodeDiffStatsMetadata(metadata)
	exitCode := 0
	if run.ExitCode != nil {
		exitCode = *run.ExitCode
	} else if run.Status == domain.TaskRunFailed {
		exitCode = 1
	}
	record := sessions.SessionRecord{
		SchemaVersion:    sessions.CurrentSchemaVersion,
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

func sessionStatusFromTaskRun(status domain.TaskRunStatus) sessions.SessionStatus {
	switch status {
	case domain.TaskRunCompleted:
		return sessions.StatusCompleted
	case domain.TaskRunFailed:
		return sessions.StatusFailed
	case domain.TaskRunCancelled:
		return sessions.StatusAborted
	default:
		return sessions.StatusRunning
	}
}

func isActiveTaskRun(status domain.TaskRunStatus) bool {
	return status == domain.TaskRunQueued || status == domain.TaskRunRunning
}

type taskRunArtifactKinds map[string]map[string]struct{}

func (s *sessionServiceImpl) taskRunArtifactKinds(
	ctx context.Context,
	wsID, taskID string,
) taskRunArtifactKinds {
	if s.artifacts == nil {
		return nil
	}
	artifactValues, err := s.artifacts.ListArtifacts(ctx, artifactsmodule.SearchQuery{
		WorkspaceKey: wsID,
		Filter: artifactsmodule.SearchFilter{
			TaskID: taskID, DurableStatus: artifactsmodule.StatusFinalized,
		},
	})
	if err != nil {
		logger.Warn("failed to load execution task run artifact flags", "workspace_id", wsID, "task_id", taskID, "err", err)
		return nil
	}
	kinds := make(taskRunArtifactKinds)
	for _, artifact := range artifactValues {
		if artifact == nil || strings.TrimSpace(artifact.WorkspaceKey) != strings.TrimSpace(wsID) ||
			strings.TrimSpace(artifact.TaskID) != strings.TrimSpace(taskID) ||
			artifact.OwnerType != artifactsmodule.OwnerTaskRun ||
			strings.TrimSpace(artifact.OwnerID) == "" ||
			artifact.DurableStatus != artifactsmodule.StatusFinalized {
			continue
		}
		if kinds[artifact.OwnerID] == nil {
			kinds[artifact.OwnerID] = make(map[string]struct{})
		}
		kinds[artifact.OwnerID][strings.TrimSpace(artifact.Type)] = struct{}{}
	}
	return kinds
}

func fillExecutionTaskRunEvidence(
	item *SessionListItem,
	run *domain.TaskRun,
	artifactKinds taskRunArtifactKinds,
) {
	if item == nil || run == nil {
		return
	}
	fillExecutionEvidence(item, run.RuntimeMetadata)
	if item.LogsRef == "" {
		item.LogsRef = run.LogsRef
	}
	item.HasTranscript = strings.TrimSpace(run.RuntimeMetadata["transcript_ref"]) != ""
	item.HasDiff = controlPlaneDiffArtifactRef(run.RuntimeMetadata) != ""
	if kinds := artifactKinds[run.TaskRunID]; kinds != nil {
		_, item.HasTranscript = kinds["transcript"]
		_, item.HasDiff = kinds["patch"]
		if strings.TrimSpace(run.RuntimeMetadata["transcript_ref"]) != "" {
			item.HasTranscript = true
		}
		if controlPlaneDiffArtifactRef(run.RuntimeMetadata) != "" {
			item.HasDiff = true
		}
	}
}

func legacyAgentSessionTaskRunID(rec *domain.AgentSession) string {
	if rec == nil || rec.Metadata == nil {
		return ""
	}
	return strings.TrimSpace(rec.Metadata["task_run_id"])
}

func (s *sessionServiceImpl) executionTaskRunTranscript(
	ctx context.Context,
	wsID string,
	run *domain.TaskRun,
) ([]transcript.Event, error) {
	transcriptRef := ""
	if run != nil {
		transcriptRef = strings.TrimSpace(run.RuntimeMetadata["transcript_ref"])
	}
	if transcriptRef == "" {
		artifactID, err := s.artifactIDForExecutionTaskRun(ctx, wsID, run, "transcript")
		if err != nil {
			return nil, err
		}
		if artifactID != "" {
			transcriptRef = "artifact://" + artifactID
		}
	}
	return loadCanonicalTranscriptArtifact(ctx, transcriptRef, func(ctx context.Context, ref string) ([]byte, error) {
		return s.readOwnedExecutionTaskRunArtifact(ctx, wsID, run, ref, "transcript")
	})
}

func (s *sessionServiceImpl) executionTaskRunDiff(
	ctx context.Context,
	wsID string,
	run *domain.TaskRun,
) (string, error) {
	artifactID := ""
	if run != nil {
		artifactID = controlPlaneDiffArtifactRef(run.RuntimeMetadata)
	}
	if artifactID == "" {
		var err error
		artifactID, err = s.artifactIDForExecutionTaskRun(ctx, wsID, run, "patch")
		if err != nil {
			return "", err
		}
	}
	artifactID = normalizeArtifactRef(artifactID)
	if artifactID == "" {
		return "", apperrors.ErrNotFound("diff not found")
	}
	data, err := s.readOwnedExecutionTaskRunArtifact(
		ctx,
		wsID,
		run,
		"artifact://"+artifactID,
		"patch",
	)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", apperrors.ErrNotFound("diff not found")
		}
		if errors.Is(err, artifactsmodule.ErrContentUnavailable) {
			return "", apperrors.ErrUnavailable("diff content is temporarily unavailable")
		}
		return "", sessionControlPlaneReadError(
			"failed to read diff",
			err,
		)
	}
	return string(data), nil
}

func (s *sessionServiceImpl) artifactIDForExecutionTaskRun(
	ctx context.Context,
	wsID string,
	run *domain.TaskRun,
	artifactType string,
) (string, error) {
	if run == nil || s.artifacts == nil {
		return "", nil
	}
	artifactValues, err := s.artifacts.ListArtifacts(ctx, artifactsmodule.SearchQuery{
		WorkspaceKey: wsID,
		Filter: artifactsmodule.SearchFilter{
			OwnerType: artifactsmodule.OwnerTaskRun, OwnerID: run.TaskRunID,
			Type: artifactType, DurableStatus: artifactsmodule.StatusFinalized,
		},
	})
	if err != nil {
		return "", sessionControlPlaneReadError(
			"failed to list task run artifacts",
			err,
		)
	}
	for _, artifact := range artifactValues {
		if artifact == nil {
			continue
		}
		if executionTaskRunArtifactMatches(artifact, run, wsID, artifact.ArtifactID, artifactType) {
			return artifact.ArtifactID, nil
		}
	}
	return "", nil
}

func (s *sessionServiceImpl) readOwnedExecutionTaskRunArtifact(
	ctx context.Context,
	wsID string,
	run *domain.TaskRun,
	ref, artifactType string,
) ([]byte, error) {
	ref = strings.TrimSpace(ref)
	if run == nil || !strings.HasPrefix(ref, "artifact://") {
		return nil, domain.ErrNotFound
	}
	artifactID := strings.TrimSpace(strings.TrimPrefix(ref, "artifact://"))
	if artifactID == "" || s.artifacts == nil {
		return nil, domain.ErrNotFound
	}
	artifact, err := s.artifacts.GetArtifact(ctx, artifactsmodule.Query{WorkspaceKey: wsID, ArtifactID: artifactID})
	if err != nil {
		return nil, err
	}
	if !executionTaskRunArtifactMatches(artifact, run, wsID, artifactID, artifactType) {
		return nil, domain.ErrNotFound
	}
	return s.readManagedArtifactContent(ctx, wsID, artifactID)
}

func executionTaskRunArtifactMatches(
	artifact *artifactsmodule.Artifact,
	run *domain.TaskRun,
	wsID, artifactID, artifactType string,
) bool {
	return artifact != nil && run != nil &&
		strings.TrimSpace(artifact.WorkspaceKey) == strings.TrimSpace(wsID) &&
		strings.TrimSpace(run.WorkspaceKey) == strings.TrimSpace(wsID) &&
		strings.TrimSpace(artifact.ArtifactID) == strings.TrimSpace(artifactID) &&
		strings.TrimSpace(artifact.TaskID) == strings.TrimSpace(run.TaskID) &&
		artifact.OwnerType == artifactsmodule.OwnerTaskRun &&
		strings.TrimSpace(artifact.OwnerID) == strings.TrimSpace(run.TaskRunID) &&
		strings.TrimSpace(artifact.Type) == strings.TrimSpace(artifactType) &&
		artifact.DurableStatus == artifactsmodule.StatusFinalized
}
