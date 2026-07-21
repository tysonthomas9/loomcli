package taskrunapi

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// sessionOpenParams is the wire-equivalent descriptor for one agent
// invocation. Attempt and all task-run linkage are deliberately absent: the
// server derives them from the fenced lease it verifies for this request.
type sessionOpenParams struct {
	InvocationKey   string                  `json:"invocationKey"`
	Backend         string                  `json:"backend"`
	Model           string                  `json:"model"`
	ParentSessionID string                  `json:"parentSessionId"`
	Kind            domain.AgentSessionKind `json:"kind"`
	Tags            []string                `json:"tags"`
	Metadata        map[string]string       `json:"metadata"`
}

type sessionOpenResult struct {
	SessionID string `json:"sessionId"`
	Attempt   int    `json:"attempt"`
}

func (m *Module) sessionOpen(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error) {
	params, err := decodeParams[sessionOpenParams](body)
	if err != nil {
		return nil, err
	}
	run, err := m.verifyLease(ctx, ws, id)
	if err != nil {
		// A terminal run also rejects the fenced heartbeat used by
		// verifyLease. Preserve that store-owned lifecycle state for this op
		// while keeping genuine stale/superseded leases as lease_denied. Open
		// still checks terminality atomically, covering the race after this
		// diagnostic read.
		if terminalRun, getErr := m.store.TaskRuns().Get(ctx, ws, id.TaskRunID); getErr == nil && terminalRun.Status.IsTerminal() {
			return nil, fmt.Errorf("open agent session: %w", &store.SessionLifecycleError{
				Code: store.SessionLifecycleErrTaskRunTerminal,
				Err:  domain.ErrConflict,
			})
		}
		return nil, err
	}
	ref, err := m.store.AgentSessions().Open(ctx, store.SessionRunContext{
		WorkspaceKey: ws,
		TaskRunID:    run.TaskRunID,
		Attempt:      taskRunAttempt(run),
		FencingToken: id.FencingToken,
		DriverRunID:  run.DriverRunID,
		DriverStepID: run.DriverStepID,
	}, store.SessionDescriptor{
		InvocationKey:   params.InvocationKey,
		Backend:         params.Backend,
		Model:           params.Model,
		ParentSessionID: params.ParentSessionID,
		Kind:            params.Kind,
		Tags:            params.Tags,
		Metadata:        params.Metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("open agent session: %w", err)
	}
	return sessionOpenResult{SessionID: ref.SessionID, Attempt: ref.Attempt}, nil
}

// sessionCloseParams supplies only the terminal invocation outcome. The
// driver runner session id intentionally lives in close metadata: the inner
// backend process cannot truthfully provide it until it has finished.
type sessionCloseParams struct {
	SessionID     string                    `json:"sessionId"`
	Status        domain.AgentSessionStatus `json:"status"`
	ExitCode      *int                      `json:"exitCode"`
	Summary       string                    `json:"summary"`
	Usage         *sessionUsageParams       `json:"usage"`
	TranscriptRef string                    `json:"transcriptRef"`
	Metadata      map[string]string         `json:"metadata"`
}

type sessionUsageParams struct {
	Tokens           *int64   `json:"tokens"`
	InputTokens      *int64   `json:"inputTokens"`
	OutputTokens     *int64   `json:"outputTokens"`
	CacheReadTokens  *int64   `json:"cacheReadTokens"`
	CacheWriteTokens *int64   `json:"cacheWriteTokens"`
	Cost             *float64 `json:"cost"`
}

type sessionCloseResult struct {
	SessionID string                    `json:"sessionId"`
	Status    domain.AgentSessionStatus `json:"status"`
}

func (m *Module) sessionClose(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error) {
	params, err := decodeParams[sessionCloseParams](body)
	if err != nil {
		return nil, err
	}
	run, err := m.verifyLease(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("sessionId required: %w", domain.ErrInvalid)
	}
	session, err := m.store.AgentSessions().Get(ctx, ws, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get agent session: %w", err)
	}
	// Do not permit one leaf to finalize an invocation owned by another task
	// run, even if it can guess that session's composed id.
	if session.TaskRunID != run.TaskRunID || session.Attempt != taskRunAttempt(run) {
		return nil, fmt.Errorf("agent session %q does not belong to task run %q: %w", sessionID, run.TaskRunID, domain.ErrNotFound)
	}
	metadata, driverRunnerSessionID := closeMetadata(params.Metadata)
	usage := store.SessionUsage{}
	if params.Usage != nil {
		usage = store.SessionUsage{
			Tokens:           params.Usage.Tokens,
			InputTokens:      params.Usage.InputTokens,
			OutputTokens:     params.Usage.OutputTokens,
			CacheReadTokens:  params.Usage.CacheReadTokens,
			CacheWriteTokens: params.Usage.CacheWriteTokens,
			CostUSD:          params.Usage.Cost,
		}
	}
	finalized, err := m.store.AgentSessions().Finalize(ctx, store.SessionRef{
		WorkspaceKey: ws,
		SessionID:    session.SessionID,
		Attempt:      session.Attempt,
	}, store.SessionOutcome{
		Status:                params.Status,
		ExitCode:              params.ExitCode,
		Summary:               params.Summary,
		TranscriptRef:         params.TranscriptRef,
		DriverRunnerSessionID: driverRunnerSessionID,
		Usage:                 usage,
		Metadata:              metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("finalize agent session: %w", err)
	}
	return sessionCloseResult{SessionID: finalized.SessionID, Status: finalized.Status}, nil
}

func closeMetadata(metadata map[string]string) (map[string]string, string) {
	if len(metadata) == 0 {
		return nil, ""
	}
	copy := make(map[string]string, len(metadata))
	for key, value := range metadata {
		copy[key] = value
	}
	driverRunnerSessionID := strings.TrimSpace(copy[store.SessionMetadataDriverRunnerSessionID])
	delete(copy, store.SessionMetadataDriverRunnerSessionID)
	if len(copy) == 0 {
		copy = nil
	}
	return copy, driverRunnerSessionID
}

// taskRunAttempt derives the current dense claim ordinal. This is deliberately
// server-side because the leaf's headers are proof of claim ownership, not an
// authority to select an attempt.
func taskRunAttempt(run *domain.TaskRun) int {
	attempt, err := strconv.Atoi(run.RuntimeMetadata["scheduler_attempt"])
	if err != nil || attempt < 0 {
		attempt = 0
	}
	return attempt + 1
}
