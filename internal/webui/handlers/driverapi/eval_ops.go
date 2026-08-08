package driverapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/evals"
	"github.com/tysonthomas9/loomcli/internal/transcriptref"
)

type statusOpError struct {
	status    int
	code      string
	message   string
	retryable bool
	details   map[string]any
}

func (e *statusOpError) Error() string { return e.message }

func writeStatusOpError(w http.ResponseWriter, err error) bool {
	var opErr *statusOpError
	if !errors.As(err, &opErr) {
		return false
	}
	writeOpErrorDetails(w, opErr.status, opErr.code, opErr.message, opErr.retryable, opErr.details)
	return true
}

func (m *Module) sessionsListUnevaluated(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		PromptVersion string `json:"promptVersion"`
	}](body)
	if err != nil {
		return nil, err
	}
	if _, err := m.verifyParent(ctx, ws, id); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.PromptVersion) == "" {
		return nil, fmt.Errorf("promptVersion required: %w", domain.ErrInvalid)
	}
	candidates, policy, err := evals.ListUnevaluated(ctx, m.store, ws, params.PromptVersion)
	if err != nil {
		return nil, err
	}
	return map[string]any{"sessions": candidates, "policy": policy}, nil
}

func (m *Module) sessionTranscriptGet(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		SessionID     string `json:"sessionId"`
		PromptVersion string `json:"promptVersion"`
	}](body)
	if err != nil {
		return nil, err
	}
	if _, err := m.verifyParent(ctx, ws, id); err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(params.SessionID)
	promptVersion := strings.TrimSpace(params.PromptVersion)
	if sessionID == "" || promptVersion == "" {
		return nil, fmt.Errorf("sessionId and promptVersion required: %w", domain.ErrInvalid)
	}
	session, err := m.store.AgentSessions().Get(ctx, ws, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session transcript metadata: %w", err)
	}
	ref := strings.TrimSpace(session.Metadata[evals.MetadataTranscriptRef])
	if ref == "" {
		return nil, fmt.Errorf("session %q missing transcript_ref: %w", sessionID, domain.ErrNotFound)
	}
	data, err := transcriptref.Resolve(ctx, m.store.Artifacts(), ws, ref)
	if err == nil {
		entries, parseErr := transcriptref.ParseTranscriptBytes(session.Metadata["backend"], data)
		if parseErr == nil {
			return map[string]any{"sessionId": sessionID, "entries": entries}, nil
		}
		err = parseErr
	}
	if _, _, stampErr := evals.PutMetric(ctx, m.store, ws, evals.PutMetricParams{
		SessionID:     sessionID,
		PromptVersion: promptVersion,
		Status:        evals.EvalStatusFailed,
		ErrorClass:    evals.ErrorTranscriptFetchFailed,
	}); stampErr != nil {
		return nil, fmt.Errorf("stamp transcript fetch failure: %w", stampErr)
	}
	return nil, &statusOpError{
		status:    http.StatusBadGateway,
		code:      evals.ErrorTranscriptFetchFailed,
		message:   fmt.Sprintf("transcript_fetch_failed: resolve transcript ref for session %q: %s", sessionID, err.Error()),
		retryable: false,
		details: map[string]any{
			"sessionId":     sessionID,
			"promptVersion": promptVersion,
			"errorClass":    evals.ErrorTranscriptFetchFailed,
		},
	}
}

func (m *Module) evalMetricPut(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		SessionID     string            `json:"sessionId"`
		PromptVersion string            `json:"promptVersion"`
		Status        string            `json:"status"`
		ErrorClass    string            `json:"errorClass"`
		Eval          evals.EvalPayload `json:"eval"`
	}](body)
	if err != nil {
		return nil, err
	}
	if _, err := m.verifyParent(ctx, ws, id); err != nil {
		return nil, err
	}
	evalID, created, err := evals.PutMetric(ctx, m.store, ws, evals.PutMetricParams{
		SessionID:     params.SessionID,
		PromptVersion: params.PromptVersion,
		Status:        params.Status,
		ErrorClass:    params.ErrorClass,
		Eval:          params.Eval,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"evalId": evalID, "created": created}, nil
}

func (m *Module) evalRejudge(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		SessionID string `json:"sessionId"`
	}](body)
	if err != nil {
		return nil, err
	}
	if _, err := m.verifyParent(ctx, ws, id); err != nil {
		return nil, err
	}
	if err := evals.Rejudge(ctx, m.store, ws, params.SessionID); err != nil {
		return nil, err
	}
	return map[string]bool{"requested": true}, nil
}
