// Workspace-scoped session read path (Traces, Phase A): task-less variants of
// the session service methods, backed by the control-plane store with
// local-store enrichment where the serve node owns the session.
package svcimpl

import (
	"context"
	"errors"
	"os"
	"sort"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func (s *sessionServiceImpl) ListWorkspaceSessions(ctx context.Context, wsID string, opts service.WorkspaceSessionListOptions) ([]service.SessionListItem, int, error) {
	records, total, err := s.listWorkspaceSessionPage(ctx, wsID, opts, opts.Limit)
	if err != nil {
		return nil, 0, err
	}
	items := s.sessionListItemsFromAgentSessions(ctx, wsID, records)
	s.attachEvalSummaries(ctx, wsID, records, items)
	return items, total, nil
}

func (s *sessionServiceImpl) ListWorkspaceSessionScoreDimensions(ctx context.Context, wsID string, opts service.WorkspaceSessionListOptions) ([]string, error) {
	records, _, err := s.listWorkspaceSessionPage(ctx, wsID, opts, 0)
	if err != nil || len(records) == 0 {
		return nil, err
	}
	bySession := sessionIDSet(records)
	evals, err := s.store.SessionEvals().List(ctx, wsID, store.SessionEvalFilter{})
	if err != nil {
		return nil, service.ErrInternal("failed to list session evals", err)
	}
	dimensions := map[string]struct{}{}
	for _, eval := range evals {
		if eval == nil || !bySession[eval.SessionID] {
			continue
		}
		for dimension := range eval.Scores {
			dimensions[dimension] = struct{}{}
		}
	}
	return sortedStringKeys(dimensions), nil
}

func (s *sessionServiceImpl) listWorkspaceSessionPage(ctx context.Context, wsID string, opts service.WorkspaceSessionListOptions, limit int) ([]*domain.AgentSession, int, error) {
	if s.store == nil {
		return nil, 0, service.ErrUnavailable("session store not available")
	}
	filter := workspaceSessionFilter(opts, limit)
	records, total, err := s.store.AgentSessions().ListPage(ctx, wsID, filter)
	if errors.Is(err, store.ErrServerCapability) {
		return nil, 0, service.ErrBadGateway("fleet-db must be upgraded: agent-sessions list response is missing total for server-side session time filtering")
	}
	if err != nil {
		return nil, 0, service.ErrInternal("failed to list sessions", err)
	}
	return records, total, nil
}

func workspaceSessionFilter(opts service.WorkspaceSessionListOptions, limit int) store.AgentSessionFilter {
	filter := store.AgentSessionFilter{AgentID: opts.AgentID, TaskRunID: opts.TaskRunID, Tags: append([]string(nil), opts.Tags...), Status: opts.Status, Kind: opts.Kind, Limit: limit}
	if !opts.Since.IsZero() {
		since := opts.Since
		filter.Since = &since
	}
	if !opts.Until.IsZero() {
		until := opts.Until
		filter.Until = &until
	}
	return filter
}

// attachEvalSummaries joins each session's eval_status metadata stamp and, when
// an eval record exists, its scores onto the list items. Best-effort: a failed
// eval-store read leaves the status stamps but no scores.

func (s *sessionServiceImpl) attachEvalSummaries(ctx context.Context, wsID string, records []*domain.AgentSession, items []service.SessionListItem) {
	statusBySession := map[string]string{}
	for _, rec := range records {
		if rec != nil && rec.Metadata != nil && rec.Metadata["eval_status"] != "" {
			statusBySession[rec.SessionID] = rec.Metadata["eval_status"]
		}
	}
	scoresBySession := map[string]*domain.SessionEvalScores{}
	if evals, err := s.store.SessionEvals().List(ctx, wsID, store.SessionEvalFilter{}); err == nil {
		for _, ev := range evals {
			if ev == nil {
				continue
			}
			if _, ok := scoresBySession[ev.SessionID]; !ok { // list is newest-first
				scores := ev.Scores
				scoresBySession[ev.SessionID] = &scores
			}
		}
	}
	for i := range items {
		sid := items[i].SessionID
		items[i].EvalStatus = statusBySession[sid]
		items[i].EvalScores = scoresBySession[sid]
	}
}

func sessionIDSet(records []*domain.AgentSession) map[string]bool {
	ids := make(map[string]bool, len(records))
	for _, record := range records {
		if record != nil {
			ids[record.SessionID] = true
		}
	}
	return ids
}

func sortedStringKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Strings(keys)
	return keys
}

func (s *sessionServiceImpl) GetWorkspaceSession(ctx context.Context, wsID, sessionID string) (*service.SessionDetailData, error) {
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return nil, service.ErrValidation("invalid session ID")
	}
	store, err := s.findStoreForSession(ctx, wsID, sessionID)
	if err != nil {
		if serviceErrorNotFound(err) {
			return s.workspaceControlPlaneSession(ctx, wsID, sessionID)
		}
		return nil, err
	}

	meta, err := store.LoadMetadata(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s.workspaceControlPlaneSession(ctx, wsID, sessionID)
		}
		logger.Error("failed to load session", "session_id", sessionID, "err", err)
		return nil, service.ErrInternal("failed to load session", err)
	}
	detail := &service.SessionDetailData{
		SessionMetadata: *meta,
		IsActive:        meta.Status == sessions.StatusRunning,
	}
	s.attachJudgeSessionLink(ctx, wsID, sessionID, detail)
	return detail, nil
}

func (s *sessionServiceImpl) workspaceControlPlaneSessionRecord(ctx context.Context, wsID, sessionID string) (*domain.AgentSession, error) {
	if s.store == nil {
		return nil, service.ErrNotFound("session not found")
	}
	rec, err := s.store.AgentSessions().Get(ctx, wsID, sessionID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, service.ErrNotFound("session not found")
		}
		return nil, service.ErrInternal("failed to load session", err)
	}
	return rec, nil
}

func (s *sessionServiceImpl) workspaceControlPlaneSession(ctx context.Context, wsID, sessionID string) (*service.SessionDetailData, error) {
	rec, err := s.workspaceControlPlaneSessionRecord(ctx, wsID, sessionID)
	if err != nil {
		return nil, err
	}
	detail := sessionDetailFromAgentSession(rec)
	s.attachJudgeSessionLink(ctx, wsID, rec.SessionID, detail)
	return detail, nil
}

func (s *sessionServiceImpl) attachJudgeSessionLink(ctx context.Context, wsID, sessionID string, detail *service.SessionDetailData) {
	if detail == nil || s.store == nil {
		return
	}
	if rec, err := s.store.AgentSessions().Get(ctx, wsID, sessionID); err == nil && rec.Metadata != nil {
		detail.JudgedSessionID = rec.Metadata["judged_session_id"]
	}
	evals, err := s.store.SessionEvals().List(ctx, wsID, store.SessionEvalFilter{SessionID: sessionID})
	if err == nil && len(evals) > 0 && evals[0] != nil {
		detail.JudgeSessionID = evals[0].JudgeSessionID
	}
}

// GetWorkspaceTraceRun returns TaskRun truth plus all joined sessions. A
// missing TaskRun is non-fatal when task-plane session evidence still exists.
func (s *sessionServiceImpl) GetWorkspaceTraceRun(ctx context.Context, wsID, taskRunID string) (*service.WorkspaceTraceRunData, error) {
	if taskRunID == "" || !validSessionID.MatchString(taskRunID) {
		return nil, service.ErrValidation("invalid task run ID")
	}
	records, _, err := s.listWorkspaceSessionPage(ctx, wsID, service.WorkspaceSessionListOptions{TaskRunID: taskRunID}, 0)
	if err != nil {
		return nil, err
	}
	items := s.sessionListItemsFromAgentSessions(ctx, wsID, records)
	s.attachEvalSummaries(ctx, wsID, records, items)
	data := traceRunData(taskRunID, records, items)
	run, err := s.store.TaskRuns().Get(ctx, wsID, taskRunID)
	if err == nil {
		return applyTaskRunTruth(data, run), nil
	}
	if errors.Is(err, domain.ErrNotFound) && len(items) > 0 {
		data.TaskRunMissing = true
		return data, nil
	}
	if errors.Is(err, domain.ErrNotFound) {
		return nil, service.ErrNotFound("task run not found")
	}
	return nil, service.ErrInternal("failed to load task run", err)
}

func traceRunData(taskRunID string, records []*domain.AgentSession, items []service.SessionListItem) *service.WorkspaceTraceRunData {
	data := &service.WorkspaceTraceRunData{TaskRunID: taskRunID, Sessions: items, AttemptCount: sessionAttemptCount(records)}
	for _, item := range items {
		data.FilesChanged += item.FilesChanged
		if data.TaskID == "" {
			data.TaskID = item.TaskID
		}
	}
	sortTraceRunSessions(data.Sessions)
	return data
}

func sortTraceRunSessions(items []service.SessionListItem) {
	sort.Slice(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.Attempt != right.Attempt {
			return left.Attempt < right.Attempt
		}
		if !left.StartedAt.Equal(right.StartedAt) {
			return left.StartedAt.Before(right.StartedAt)
		}
		if left.InvocationKey != right.InvocationKey {
			return left.InvocationKey < right.InvocationKey
		}
		return left.SessionID < right.SessionID
	})
}

func sessionAttemptCount(records []*domain.AgentSession) int {
	maxAttempt := -1
	for _, record := range records {
		if record != nil && record.Attempt > maxAttempt {
			maxAttempt = record.Attempt
		}
	}
	return maxAttempt + 1
}

func applyTaskRunTruth(data *service.WorkspaceTraceRunData, run *domain.TaskRun) *service.WorkspaceTraceRunData {
	data.TaskRun = run
	data.TaskRunMissing = false
	if run == nil {
		return data
	}
	data.TaskID = run.TaskID
	data.TotalTokens = run.InputTokens + run.OutputTokens + run.CacheReadTokens + run.CacheWriteTokens
	if !run.StartedAt.IsZero() && run.FinishedAt != nil {
		data.DurationSeconds = run.FinishedAt.Sub(run.StartedAt).Seconds()
	}
	return data
}

func (s *sessionServiceImpl) GetWorkspaceSessionTranscript(ctx context.Context, wsID, sessionID string) ([]transcript.Event, error) {
	store, _, err := s.workspaceSessionStore(ctx, wsID, sessionID)
	if err != nil {
		if !serviceErrorNotFound(err) {
			return nil, err
		}
		return s.workspaceControlPlaneSessionTranscript(ctx, wsID, sessionID)
	}
	if evs, ok := eventStoreParentEvents(store, sessionID); ok {
		return evs, nil
	}
	events, loadErr := store.LoadNativeEvents(sessionID)
	if loadErr != nil {
		if cpEvents, cpErr := s.workspaceControlPlaneSessionTranscript(ctx, wsID, sessionID); cpErr == nil {
			return cpEvents, nil
		}
		logger.Error("failed to load native transcript", "session_id", sessionID, "err", loadErr)
		return nil, service.ErrInternal("failed to load transcript", loadErr)
	}
	if events == nil {
		events = []transcript.Event{}
	}
	return events, nil
}

func (s *sessionServiceImpl) workspaceControlPlaneSessionTranscript(ctx context.Context, wsID, sessionID string) ([]transcript.Event, error) {
	rec, err := s.workspaceControlPlaneSessionRecord(ctx, wsID, sessionID)
	if err != nil {
		return nil, err
	}
	return s.controlPlaneTranscriptFromRecord(ctx, wsID, rec)
}

func (s *sessionServiceImpl) ListWorkspaceSessionSubagents(ctx context.Context, wsID, sessionID string) ([]string, error) {
	store, _, err := s.workspaceSessionStore(ctx, wsID, sessionID)
	if err != nil {
		return nil, err
	}
	return listSessionSubagentsFromStore(store, sessionID)
}

func (s *sessionServiceImpl) GetWorkspaceSessionSubagentTranscript(ctx context.Context, wsID, sessionID, subagentID string) ([]transcript.Event, error) {
	store, meta, err := s.workspaceSessionStore(ctx, wsID, sessionID)
	if err != nil {
		return nil, err
	}
	return getSessionSubagentTranscriptFromStore(store, meta, sessionID, subagentID)
}

func (s *sessionServiceImpl) GetWorkspaceSessionDiff(ctx context.Context, wsID, sessionID string) (string, error) {
	store, _, err := s.workspaceSessionStore(ctx, wsID, sessionID)
	if err != nil {
		if !serviceErrorNotFound(err) {
			return "", err
		}
		return s.workspaceControlPlaneSessionDiff(ctx, wsID, sessionID)
	}

	diff, diffErr := store.ReadDiff(sessionID)
	if diffErr != nil {
		if errors.Is(diffErr, os.ErrNotExist) {
			cpDiff, cpErr := s.workspaceControlPlaneSessionDiff(ctx, wsID, sessionID)
			if cpErr == nil {
				return cpDiff, nil
			}
			if serviceErrorNotFound(cpErr) {
				return "", service.ErrNotFound("diff not found")
			}
			return "", cpErr
		}
		logger.Error("failed to read diff", "session_id", sessionID, "err", diffErr)
		return "", service.ErrInternal("failed to read diff", diffErr)
	}
	if diff == "" {
		if cpDiff, cpErr := s.workspaceControlPlaneSessionDiff(ctx, wsID, sessionID); cpErr == nil {
			return cpDiff, nil
		}
	}
	return diff, nil
}
