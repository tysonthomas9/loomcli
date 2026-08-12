package driverapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
)

func (m *Module) issueBackendForRun(ctx context.Context, ws string, id driverIdentity) (backend.IssueBackend, string, error) {
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, "", err
	}
	actor := driverpkg.DriverRunActor(parent.RunID)
	issueBackend, err := m.issueBackends(ws, actor)
	if err != nil {
		return nil, "", err
	}
	return issueBackend, actor, nil
}

func (m *Module) issueGet(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		IssueID string `json:"issueId"`
	}](body)
	if err != nil {
		return nil, err
	}
	issueBackend, _, err := m.issueBackendForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.IssueID) == "" {
		return nil, fmt.Errorf("issueId required: %w", domain.ErrInvalid)
	}
	return issueBackend.Get(ctx, params.IssueID)
}

func (m *Module) issueList(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		ExternalRef string `json:"externalRef"`
		Type        string `json:"type"`
		Status      string `json:"status"`
		Limit       int    `json:"limit"`
	}](body)
	if err != nil {
		return nil, err
	}
	issueBackend, _, err := m.issueBackendForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	return issueBackend.List(ctx, backend.ListOpts{ExternalRef: params.ExternalRef, IssueType: params.Type, Status: params.Status, Limit: limit})
}

func (m *Module) issueListComments(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		IssueID string `json:"issueId"`
	}](body)
	if err != nil {
		return nil, err
	}
	issueBackend, _, err := m.issueBackendForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.IssueID) == "" {
		return nil, fmt.Errorf("issueId required: %w", domain.ErrInvalid)
	}
	return issueBackend.ListComments(ctx, params.IssueID)
}

func (m *Module) issueComment(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		IssueID string `json:"issueId"`
		Body    string `json:"body"`
	}](body)
	if err != nil {
		return nil, err
	}
	issueBackend, actor, err := m.issueBackendForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.IssueID) == "" || strings.TrimSpace(params.Body) == "" {
		return nil, fmt.Errorf("issueId and body required: %w", domain.ErrInvalid)
	}
	return issueBackend.AddComment(ctx, backend.CommentAddParams{IssueID: params.IssueID, Author: actor, Text: params.Body})
}

func (m *Module) issueUpdate(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		IssueID     string   `json:"issueId"`
		Status      *string  `json:"status"`
		Priority    *int     `json:"priority"`
		Labels      []string `json:"labels"`
		Assignee    *string  `json:"assignee"`
		ExternalRef *string  `json:"externalRef"`
	}](body)
	if err != nil {
		return nil, err
	}
	issueBackend, _, err := m.issueBackendForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.IssueID) == "" {
		return nil, fmt.Errorf("issueId required: %w", domain.ErrInvalid)
	}
	update := backend.UpdateParams{Status: params.Status, Priority: params.Priority, Assignee: params.Assignee, ExternalRef: params.ExternalRef}
	if params.Labels != nil {
		update.SetLabels = params.Labels
	}
	if err := issueBackend.Update(ctx, params.IssueID, update); err != nil {
		return nil, fmt.Errorf("update issue: %w", err)
	}
	return map[string]any{"id": params.IssueID}, nil
}

func (m *Module) issueAddLabel(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	return m.issueLabelOp(ctx, ws, id, body, true)
}

func (m *Module) issueRemoveLabel(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	return m.issueLabelOp(ctx, ws, id, body, false)
}

func (m *Module) issueLabelOp(ctx context.Context, ws string, id driverIdentity, body []byte, add bool) (any, error) {
	params, err := decodeParams[struct {
		IssueID string `json:"issueId"`
		Label   string `json:"label"`
	}](body)
	if err != nil {
		return nil, err
	}
	issueBackend, _, err := m.issueBackendForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.IssueID) == "" || strings.TrimSpace(params.Label) == "" {
		return nil, fmt.Errorf("issueId and label required: %w", domain.ErrInvalid)
	}
	if add {
		err = issueBackend.AddLabel(ctx, params.IssueID, params.Label)
	} else {
		err = issueBackend.RemoveLabel(ctx, params.IssueID, params.Label)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": params.IssueID, "label": params.Label}, nil
}
