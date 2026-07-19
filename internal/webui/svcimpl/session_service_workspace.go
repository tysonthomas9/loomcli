// Workspace-scoped session read path (Traces, Phase A): task-less variants of
// the session service methods, backed by the control-plane store with
// local-store enrichment where the serve node owns the session.
package svcimpl

import (
	"context"
	"errors"
	"os"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func (s *sessionServiceImpl) ListWorkspaceSessions(ctx context.Context, wsID string, opts service.WorkspaceSessionListOptions) ([]service.SessionListItem, int, error) {
	if s.store == nil {
		return nil, 0, service.ErrUnavailable("session store not available")
	}
	filter := store.AgentSessionFilter{
		AgentID: opts.AgentID,
		Status:  opts.Status,
		Kind:    opts.Kind,
		Limit:   opts.Limit,
	}
	if !opts.Since.IsZero() {
		since := opts.Since
		filter.Since = &since
	}
	if !opts.Until.IsZero() {
		until := opts.Until
		filter.Until = &until
	}
	records, total, err := s.store.AgentSessions().ListPage(ctx, wsID, filter)
	if err != nil {
		if errors.Is(err, store.ErrServerCapability) {
			return nil, 0, service.ErrBadGateway("fleet-db must be upgraded: agent-sessions list response is missing total for server-side session time filtering")
		}
		return nil, 0, service.ErrInternal("failed to list sessions", err)
	}
	items := s.sessionListItemsFromAgentSessions(ctx, wsID, records)
	s.attachEvalSummaries(ctx, wsID, records, items)
	return items, total, nil
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
	if len(statusBySession) == 0 {
		return
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
	return &service.SessionDetailData{
		SessionMetadata: *meta,
		IsActive:        meta.Status == sessions.StatusRunning,
	}, nil
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
	return sessionDetailFromAgentSession(rec), nil
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
