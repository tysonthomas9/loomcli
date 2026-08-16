package driverapi

import (
	"context"
	"fmt"
	"strings"

	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

func (m *Module) workItemsForRun(
	ctx context.Context,
	ws string,
	id driverIdentity,
) (WorkItemOperations, string, error) {
	if m == nil || m.workItems == nil {
		return nil, "", fmt.Errorf("driver Work Items capability is not configured: %w", workitems.ErrUnavailable)
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, "", err
	}
	return m.workItems, driverpkg.DriverRunActor(parent.RunID), nil
}

func (m *Module) issueGet(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		IssueID string `json:"issueId"`
	}](body)
	if err != nil {
		return nil, err
	}
	items, _, err := m.workItemsForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.IssueID) == "" {
		return nil, fmt.Errorf("issueId required: %w", persistence.ErrInvalid)
	}
	return items.Get(ctx, workitems.GetQuery{IssueID: params.IssueID})
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
	items, _, err := m.workItemsForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	result, err := items.List(ctx, workitems.ListQuery{Filter: workitems.ListFilter{
		ExternalRef: params.ExternalRef,
		IssueType:   params.Type,
		Status:      params.Status,
		Limit:       limit,
	}})
	if err != nil {
		return nil, err
	}
	issues := make([]workitems.IssueSummary, 0, len(result.Issues))
	for _, issue := range result.Issues {
		issues = append(issues, issue.IssueSummary)
	}
	return issues, nil
}

func (m *Module) issueListComments(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		IssueID string `json:"issueId"`
	}](body)
	if err != nil {
		return nil, err
	}
	items, _, err := m.workItemsForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.IssueID) == "" {
		return nil, fmt.Errorf("issueId required: %w", persistence.ErrInvalid)
	}
	return items.ListComments(ctx, workitems.ListCommentsQuery{IssueID: params.IssueID})
}

func (m *Module) issueComment(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		IssueID string `json:"issueId"`
		Body    string `json:"body"`
	}](body)
	if err != nil {
		return nil, err
	}
	items, actor, err := m.workItemsForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.IssueID) == "" || strings.TrimSpace(params.Body) == "" {
		return nil, fmt.Errorf("issueId and body required: %w", persistence.ErrInvalid)
	}
	return items.AddComment(ctx, workitems.AddCommentCommand{
		IssueID: params.IssueID,
		Author:  actor,
		Text:    params.Body,
	})
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
	items, _, err := m.workItemsForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.IssueID) == "" {
		return nil, fmt.Errorf("issueId required: %w", persistence.ErrInvalid)
	}
	command := workitems.PatchCommand{
		IssueID: params.IssueID, Status: params.Status, Priority: params.Priority,
		Assignee: params.Assignee, ExternalRef: params.ExternalRef,
	}
	if params.Labels != nil {
		command.SetLabels = params.Labels
	}
	if _, err := items.Patch(ctx, command); err != nil {
		return nil, fmt.Errorf("update issue: %w", err)
	}
	return map[string]any{"id": params.IssueID}, nil
}

func (m *Module) issueBlockRepositoryRequired(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		IssueID string `json:"issueId"`
	}](body)
	if err != nil {
		return nil, err
	}
	items, _, err := m.workItemsForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.IssueID) == "" {
		return nil, fmt.Errorf("issueId required: %w", persistence.ErrInvalid)
	}
	result, err := items.BlockRepositoryRequired(ctx, workitems.BlockRepositoryRequiredCommand{IssueID: params.IssueID})
	if err != nil {
		return nil, fmt.Errorf("block repository-required issue: %w", err)
	}
	return result, nil
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
	items, _, err := m.workItemsForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.IssueID) == "" || strings.TrimSpace(params.Label) == "" {
		return nil, fmt.Errorf("issueId and label required: %w", persistence.ErrInvalid)
	}
	command := workitems.PatchCommand{IssueID: params.IssueID}
	if add {
		command.AddLabels = []string{params.Label}
	} else {
		command.RemoveLabels = []string{params.Label}
	}
	if _, err := items.Patch(ctx, command); err != nil {
		return nil, err
	}
	return map[string]any{"id": params.IssueID, "label": params.Label}, nil
}
