package fleetdb

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func (s *automationStore) CreateBinding(ctx context.Context, binding *domain.TriggerBinding) (*domain.TriggerBinding, error) {
	if binding == nil || binding.WebhookSecret != "" {
		return nil, fmt.Errorf("automation create binding rejects webhook_secret: %w", ErrAutomationInvalid)
	}
	if strings.TrimSpace(binding.TargetAgentServiceID) != "" {
		return nil, ErrAutomationManagedBindingConflict
	}
	var out domain.TriggerBinding
	path := "/api/v1/" + pathEscape(binding.WorkspaceKey) + "/trigger-bindings"
	if err := s.client.do(ctx, http.MethodPost, path, automationBindingBody(binding, true), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *automationStore) GetBinding(ctx context.Context, workspace, bindingID string) (*domain.TriggerBinding, error) {
	var out domain.TriggerBinding
	path := "/api/v1/" + pathEscape(workspace) + "/trigger-bindings/" + pathEscape(bindingID)
	if err := s.client.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *automationStore) ListBindings(ctx context.Context, workspace string, filter AutomationBindingFilter) ([]*domain.TriggerBinding, error) {
	query := url.Values{}
	queryValue(query, "source_kind", filter.SourceKind)
	queryValue(query, "route_key", filter.RouteKey)
	queryValue(query, "driver_id", filter.DriverID)
	queryValue(query, "target_agent_service_id", filter.TargetAgentServiceID)
	if filter.Enabled != nil {
		query.Set("enabled", strconv.FormatBool(*filter.Enabled))
	}
	queryLimit(query, filter.Limit)
	var out struct {
		Bindings []*domain.TriggerBinding `json:"trigger_bindings"`
	}
	path := withQuery("/api/v1/"+pathEscape(workspace)+"/trigger-bindings", query)
	if err := s.client.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out.Bindings == nil {
		out.Bindings = []*domain.TriggerBinding{}
	}
	return out.Bindings, nil
}

func (s *automationStore) UpdateBinding(ctx context.Context, binding *domain.TriggerBinding) (*domain.TriggerBinding, error) {
	if binding == nil || binding.WebhookSecret != "" {
		return nil, fmt.Errorf("automation update binding rejects webhook_secret: %w", ErrAutomationInvalid)
	}
	var out domain.TriggerBinding
	path := "/api/v1/" + pathEscape(binding.WorkspaceKey) + "/trigger-bindings/" + pathEscape(binding.BindingID)
	if err := s.client.do(ctx, http.MethodPatch, path, automationBindingBody(binding, false), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *automationStore) DeleteBinding(ctx context.Context, workspace, bindingID string) error {
	path := "/api/v1/" + pathEscape(workspace) + "/trigger-bindings/" + pathEscape(bindingID)
	return s.client.do(ctx, http.MethodDelete, path, nil, nil)
}

func (s *automationStore) ReplaceUnmanagedBinding(ctx context.Context, replacement AutomationUnmanagedBindingReplacement) (*domain.TriggerBinding, error) {
	if err := validateAutomationUnmanagedBindingSnapshot(replacement.Expected); err != nil {
		return nil, err
	}
	if replacement.Binding == nil || replacement.Binding.WebhookSecret != "" ||
		strings.TrimSpace(replacement.Binding.TargetAgentServiceID) != "" {
		return nil, fmt.Errorf("automation unmanaged binding replacement rejects nil binding, owner, or webhook_secret: %w", ErrAutomationInvalid)
	}
	if replacement.Binding.WorkspaceKey != replacement.Expected.WorkspaceKey ||
		replacement.Binding.BindingID != replacement.Expected.BindingID ||
		!replacement.Binding.CreatedAt.Equal(replacement.Expected.ExpectedCreatedAt) ||
		!replacement.Binding.UpdatedAt.After(replacement.Expected.ExpectedUpdatedAt) {
		return nil, fmt.Errorf("automation unmanaged binding replacement identity is invalid: %w", ErrAutomationInvalid)
	}
	body := map[string]any{
		"expected_route_key":  replacement.Expected.ExpectedRouteKey,
		"expected_created_at": replacement.Expected.ExpectedCreatedAt,
		"expected_updated_at": replacement.Expected.ExpectedUpdatedAt,
		"binding":             replacement.Binding,
	}
	var out domain.TriggerBinding
	path := "/api/v1/" + pathEscape(replacement.Expected.WorkspaceKey) + "/automation/unmanaged-bindings/" +
		pathEscape(replacement.Expected.BindingID) + "/replace"
	if err := s.client.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *automationStore) DeleteUnmanagedBindingIfUnchanged(ctx context.Context, expected AutomationUnmanagedBindingSnapshot) error {
	if err := validateAutomationUnmanagedBindingSnapshot(expected); err != nil {
		return err
	}
	body := map[string]any{
		"expected_route_key":  expected.ExpectedRouteKey,
		"expected_created_at": expected.ExpectedCreatedAt,
		"expected_updated_at": expected.ExpectedUpdatedAt,
	}
	path := "/api/v1/" + pathEscape(expected.WorkspaceKey) + "/automation/unmanaged-bindings/" +
		pathEscape(expected.BindingID) + "/delete"
	return s.client.do(ctx, http.MethodPost, path, body, nil)
}

func validateAutomationUnmanagedBindingSnapshot(expected AutomationUnmanagedBindingSnapshot) error {
	if !automationWorkspaceKeyValid(expected.WorkspaceKey) || !automationCanonical(expected.BindingID) ||
		expected.ExpectedRouteKey != strings.TrimSpace(expected.ExpectedRouteKey) ||
		expected.ExpectedCreatedAt.IsZero() || expected.ExpectedUpdatedAt.IsZero() {
		return fmt.Errorf("automation unmanaged binding snapshot is invalid: %w", ErrAutomationInvalid)
	}
	return nil
}

func (s *automationStore) CreateManagedBinding(ctx context.Context, binding *domain.TriggerBinding) (*domain.TriggerBinding, error) {
	if binding == nil || binding.WebhookSecret != "" || strings.TrimSpace(binding.TargetAgentServiceID) == "" {
		return nil, fmt.Errorf("automation managed binding create requires owner and rejects webhook_secret: %w", ErrAutomationInvalid)
	}
	var out domain.TriggerBinding
	path := "/api/v1/" + pathEscape(binding.WorkspaceKey) + "/automation/managed-bindings"
	if err := s.client.do(ctx, http.MethodPost, path, binding, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *automationStore) ReplaceManagedBinding(ctx context.Context, replacement AutomationManagedBindingReplacement) (*domain.TriggerBinding, error) {
	if err := validateAutomationManagedBindingSnapshot(replacement.Expected); err != nil {
		return nil, err
	}
	if replacement.Binding == nil || replacement.Binding.WebhookSecret != "" {
		return nil, fmt.Errorf("automation managed binding replacement rejects nil binding or webhook_secret: %w", ErrAutomationInvalid)
	}
	if replacement.Binding.WorkspaceKey != replacement.Expected.WorkspaceKey ||
		replacement.Binding.BindingID != replacement.Expected.BindingID ||
		replacement.Binding.TargetAgentServiceID != replacement.Expected.ExpectedTargetAgentServiceID ||
		!replacement.Binding.CreatedAt.Equal(replacement.Expected.ExpectedCreatedAt) ||
		!replacement.Binding.UpdatedAt.After(replacement.Expected.ExpectedUpdatedAt) {
		return nil, fmt.Errorf("automation managed binding replacement identity is invalid: %w", ErrAutomationInvalid)
	}
	body := map[string]any{
		"expected_target_agent_service_id": replacement.Expected.ExpectedTargetAgentServiceID,
		"expected_route_key":               replacement.Expected.ExpectedRouteKey,
		"expected_created_at":              replacement.Expected.ExpectedCreatedAt,
		"expected_updated_at":              replacement.Expected.ExpectedUpdatedAt,
		"binding":                          replacement.Binding,
	}
	var out domain.TriggerBinding
	path := "/api/v1/" + pathEscape(replacement.Expected.WorkspaceKey) + "/automation/managed-bindings/" +
		pathEscape(replacement.Expected.BindingID) + "/replace"
	if err := s.client.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *automationStore) DeleteManagedBindingIfUnchanged(ctx context.Context, expected AutomationManagedBindingSnapshot) error {
	if err := validateAutomationManagedBindingSnapshot(expected); err != nil {
		return err
	}
	body := map[string]any{
		"expected_target_agent_service_id": expected.ExpectedTargetAgentServiceID,
		"expected_route_key":               expected.ExpectedRouteKey,
		"expected_created_at":              expected.ExpectedCreatedAt,
		"expected_updated_at":              expected.ExpectedUpdatedAt,
	}
	path := "/api/v1/" + pathEscape(expected.WorkspaceKey) + "/automation/managed-bindings/" +
		pathEscape(expected.BindingID) + "/delete"
	return s.client.do(ctx, http.MethodPost, path, body, nil)
}

func validateAutomationManagedBindingSnapshot(expected AutomationManagedBindingSnapshot) error {
	if !automationWorkspaceKeyValid(expected.WorkspaceKey) || !automationCanonical(expected.BindingID) ||
		!automationCanonical(expected.ExpectedTargetAgentServiceID) ||
		expected.ExpectedRouteKey != strings.TrimSpace(expected.ExpectedRouteKey) ||
		expected.ExpectedCreatedAt.IsZero() || expected.ExpectedUpdatedAt.IsZero() {
		return fmt.Errorf("automation managed binding snapshot is invalid: %w", ErrAutomationInvalid)
	}
	return nil
}

func (s *automationStore) MatchBindings(ctx context.Context, workspace, routeKey string) (*AutomationBindingMatchSnapshot, error) {
	var out AutomationBindingMatchSnapshot
	path := "/api/v1/" + pathEscape(workspace) + "/automation/binding-matches/" + pathEscape(routeKey)
	if err := s.client.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out.Bindings == nil {
		out.Bindings = []*domain.TriggerBinding{}
	}
	return &out, nil
}
