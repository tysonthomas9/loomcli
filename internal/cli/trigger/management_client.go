package trigger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/httpclient"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

const triggerManagementResponseLimit = 4 << 20

type triggerHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// triggerManagementClient is the standalone CLI adapter for Automation's
// management and audit HTTP routes. It never opens a Store or starts serve;
// both endpoint and workspace selection must be explicit.
type triggerManagementClient struct {
	serverURL string
	workspace string
	doer      triggerHTTPDoer
}

type triggerBindingsAPIResponse struct {
	Bindings []*automation.Binding `json:"bindings"`
}

type triggerEventsAPIResponse struct {
	Events []*automation.Event `json:"trigger_events"`
	Count  int                 `json:"count"`
}

type triggerDeliveriesAPIResponse struct {
	Deliveries []*automation.Delivery `json:"trigger_deliveries"`
	Count      int                    `json:"count"`
}

// triggerBindingCreateRequest intentionally mirrors the long-standing CLI
// flag surface rather than the UI's smaller form. The management handler must
// preserve every field; the client never falls back to direct persistence.
type triggerBindingCreateRequest struct {
	Workflow            string                              `json:"workflow,omitempty"`
	DriverID            string                              `json:"driver_id,omitempty"`
	DriverVersionID     string                              `json:"driver_version_id,omitempty"`
	RouteKey            string                              `json:"route_key"`
	SourceKind          string                              `json:"source_kind,omitempty"`
	Name                string                              `json:"name,omitempty"`
	BindingID           string                              `json:"binding_id,omitempty"`
	Entrypoint          string                              `json:"entrypoint,omitempty"`
	EventTypePatterns   []string                            `json:"event_type_patterns,omitempty"`
	Enabled             *bool                               `json:"enabled,omitempty"`
	SubjectKeyTemplate  string                              `json:"subject_key_template,omitempty"`
	ConcurrencyPolicy   automation.BindingConcurrencyPolicy `json:"concurrency_policy,omitempty"`
	ActorFilter         *automation.ActorFilter             `json:"actor_filter,omitempty"`
	RetryMaxAttempts    int                                 `json:"retry_max_attempts,omitempty"`
	RetryBackoffSeconds int                                 `json:"retry_backoff_seconds,omitempty"`
	Schedule            string                              `json:"schedule,omitempty"`
	ScheduleTimezone    string                              `json:"schedule_timezone,omitempty"`
}

// triggerBindingPatchRequest retains omitted-versus-explicit-zero semantics.
// clear_actor_filter represents the legacy CLI's single-empty-value clear.
type triggerBindingPatchRequest struct {
	EventTypePatterns   *[]string                            `json:"event_type_patterns,omitempty"`
	SubjectKeyTemplate  *string                              `json:"subject_key_template,omitempty"`
	ConcurrencyPolicy   *automation.BindingConcurrencyPolicy `json:"concurrency_policy,omitempty"`
	ActorFilter         *automation.ActorFilter              `json:"actor_filter,omitempty"`
	ClearActorFilter    bool                                 `json:"clear_actor_filter,omitempty"`
	RetryMaxAttempts    *int                                 `json:"retry_max_attempts,omitempty"`
	RetryBackoffSeconds *int                                 `json:"retry_backoff_seconds,omitempty"`
	Schedule            *string                              `json:"schedule,omitempty"`
	ScheduleTimezone    *string                              `json:"schedule_timezone,omitempty"`
}

type deleteBindingAPIResponse struct {
	BindingID     string `json:"binding_id"`
	Deleted       bool   `json:"deleted"`
	GrantsRevoked int    `json:"grants_revoked"`
}

type triggerManagementAPIError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Code    string `json:"code"`
	Kind    string `json:"kind"`
}

func newTriggerManagementClient(_ context.Context) (*triggerManagementClient, error) {
	serverURL := strings.TrimRight(strings.TrimSpace(os.Getenv("LOOM_SERVER_URL")), "/")
	if serverURL == "" {
		return nil, fmt.Errorf("loom trigger management commands require --server or LOOM_SERVER_URL")
	}
	parsed, err := url.Parse(serverURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("invalid Loom management server URL %q", serverURL)
	}
	workspace := strings.TrimSpace(os.Getenv("LOOM_WORKSPACE"))
	if workspace == "" {
		return nil, fmt.Errorf("loom trigger management commands require --workspace or LOOM_WORKSPACE")
	}

	authClient, err := httpclient.New(httpclient.Config{
		ServerURL: serverURL,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	})
	if err != nil {
		return nil, fmt.Errorf("trigger management endpoint discovery: %w", err)
	}
	client := &triggerManagementClient{
		serverURL: serverURL,
		workspace: workspace,
		doer:      authClient,
	}
	return client, nil
}

func (c *triggerManagementClient) listBindings(ctx context.Context) ([]*automation.Binding, error) {
	var out triggerBindingsAPIResponse
	if err := c.doJSON(ctx, http.MethodGet, c.workspacePath("/trigger-bindings"), nil, &out, nil); err != nil {
		return nil, err
	}
	return out.Bindings, nil
}

func (c *triggerManagementClient) getBinding(ctx context.Context, bindingID string) (*automation.Binding, error) {
	bindings, err := c.listBindings(ctx)
	if err != nil {
		return nil, err
	}
	bindingID = strings.TrimSpace(bindingID)
	for _, binding := range bindings {
		if binding != nil && binding.BindingID == bindingID {
			return binding, nil
		}
	}
	return nil, fmt.Errorf("trigger binding %q: %w", bindingID, persistence.ErrNotFound)
}

func (c *triggerManagementClient) createBinding(ctx context.Context, input triggerBindingCreateRequest) (*automation.Binding, error) {
	var out automation.Binding
	if err := c.doJSON(ctx, http.MethodPost, c.workspacePath("/trigger-bindings")+"?create_only=true", input, &out, nil); err != nil {
		return nil, err
	}
	return &out, validateTriggerBindingResponse(&out)
}

func (c *triggerManagementClient) updateBinding(ctx context.Context, bindingID string, patch triggerBindingPatchRequest) (*automation.Binding, error) {
	var out automation.Binding
	path := c.workspacePath("/trigger-bindings/" + url.PathEscape(strings.TrimSpace(bindingID)))
	if err := c.doJSON(ctx, http.MethodPatch, path, patch, &out, nil); err != nil {
		return nil, err
	}
	return &out, validateTriggerBindingResponse(&out)
}

func (c *triggerManagementClient) deleteBinding(ctx context.Context, bindingID string) (*deleteBindingAPIResponse, error) {
	var out deleteBindingAPIResponse
	path := c.workspacePath("/trigger-bindings/" + url.PathEscape(strings.TrimSpace(bindingID)))
	if err := c.doJSON(ctx, http.MethodDelete, path, nil, &out, nil); err != nil {
		return nil, err
	}
	if !out.Deleted || strings.TrimSpace(out.BindingID) == "" {
		return nil, fmt.Errorf("trigger management API returned an incomplete delete result")
	}
	return &out, nil
}

func (c *triggerManagementClient) runBinding(ctx context.Context, bindingID string) (*execution.DriverRunRecord, error) {
	var out execution.DriverRunRecord
	path := c.workspacePath("/trigger-bindings/" + url.PathEscape(strings.TrimSpace(bindingID)) + "/run")
	if err := c.doJSON(ctx, http.MethodPost, path, nil, &out, nil); err != nil {
		return nil, err
	}
	if strings.TrimSpace(out.RunID) == "" {
		return nil, fmt.Errorf("trigger management API returned an incomplete run result")
	}
	return &out, nil
}

func (c *triggerManagementClient) listEvents(ctx context.Context, sourceKind string, limit int) ([]*automation.Event, error) {
	query := url.Values{}
	if sourceKind = strings.TrimSpace(sourceKind); sourceKind != "" {
		query.Set("source_kind", sourceKind)
	}
	if limit > 0 {
		query.Set("limit", fmt.Sprint(limit))
	}
	var out triggerEventsAPIResponse
	if err := c.doJSON(ctx, http.MethodGet, c.workspacePath("/trigger-events"), nil, &out, query); err != nil {
		return nil, err
	}
	return out.Events, nil
}

func (c *triggerManagementClient) getEvent(ctx context.Context, eventID string) (*automation.Event, error) {
	var out automation.Event
	path := c.workspacePath("/trigger-events/" + url.PathEscape(strings.TrimSpace(eventID)))
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out, nil); err != nil {
		return nil, err
	}
	if strings.TrimSpace(out.EventID) == "" {
		return nil, fmt.Errorf("trigger management API returned an incomplete event result")
	}
	return &out, nil
}

func (c *triggerManagementClient) listDeliveries(ctx context.Context, eventID, status string, limit int) ([]*automation.Delivery, error) {
	query := url.Values{}
	if eventID = strings.TrimSpace(eventID); eventID != "" {
		query.Set("trigger_event_id", eventID)
	}
	if status = strings.TrimSpace(status); status != "" {
		query.Set("status", status)
	}
	if limit > 0 {
		query.Set("limit", fmt.Sprint(limit))
	}
	var out triggerDeliveriesAPIResponse
	if err := c.doJSON(ctx, http.MethodGet, c.workspacePath("/trigger-deliveries"), nil, &out, query); err != nil {
		return nil, err
	}
	return out.Deliveries, nil
}

func (c *triggerManagementClient) getDelivery(ctx context.Context, deliveryID string) (*automation.Delivery, error) {
	var out automation.Delivery
	path := c.workspacePath("/trigger-deliveries/" + url.PathEscape(strings.TrimSpace(deliveryID)))
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out, nil); err != nil {
		return nil, err
	}
	if strings.TrimSpace(out.DeliveryID) == "" {
		return nil, fmt.Errorf("trigger management API returned an incomplete delivery result")
	}
	return &out, nil
}

func validateTriggerBindingResponse(binding *automation.Binding) error {
	if binding == nil || strings.TrimSpace(binding.BindingID) == "" || strings.TrimSpace(binding.WorkspaceKey) == "" {
		return fmt.Errorf("trigger management API returned an incomplete binding result")
	}
	return nil
}

func (c *triggerManagementClient) workspacePath(suffix string) string {
	return "/api/workspaces/" + url.PathEscape(c.workspace) + suffix
}

func (c *triggerManagementClient) doJSON(ctx context.Context, method, path string, input, output any, query url.Values) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode trigger management request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	requestURL := c.serverURL + path
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return fmt.Errorf("build trigger management request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.doer.Do(req)
	if err != nil {
		return fmt.Errorf("trigger management endpoint unavailable at %s: %w", c.serverURL, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, triggerManagementResponseLimit+1))
	if err != nil {
		return fmt.Errorf("read trigger management response: %w", err)
	}
	if len(data) > triggerManagementResponseLimit {
		return fmt.Errorf("trigger management response exceeds %d bytes", triggerManagementResponseLimit)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return triggerManagementStatusError(resp.StatusCode, data)
	}
	if output == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode trigger management response: %w", err)
	}
	return nil
}

func triggerManagementStatusError(status int, data []byte) error {
	var payload triggerManagementAPIError
	_ = json.Unmarshal(data, &payload)
	message := strings.TrimSpace(payload.Error)
	if message == "" {
		message = strings.TrimSpace(payload.Message)
	}
	if message == "" {
		message = strings.TrimSpace(string(data))
	}
	if message == "" {
		message = http.StatusText(status)
	}
	detail := fmt.Sprintf("trigger management API HTTP %d: %s", status, message)
	if payload.Code != "" {
		detail += " (code=" + payload.Code + ")"
	} else if payload.Kind != "" {
		detail += " (kind=" + payload.Kind + ")"
	}
	switch status {
	case http.StatusBadRequest, http.StatusPreconditionRequired, http.StatusPreconditionFailed:
		return fmt.Errorf("%s: %w", detail, persistence.ErrInvalid)
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", detail, persistence.ErrNotFound)
	case http.StatusConflict:
		return fmt.Errorf("%s: %w", detail, persistence.ErrConflict)
	case http.StatusUnauthorized:
		return errors.New("trigger management API unauthorized: " + detail)
	case http.StatusForbidden:
		return errors.New("trigger management API forbidden: " + detail)
	default:
		return errors.New(detail)
	}
}
