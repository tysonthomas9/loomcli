package workflow

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

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/httpclient"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

const workflowManagementResponseLimit = 4 << 20

type workflowHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// workflowManagementClient is the standalone CLI adapter for Workflow Catalog.
// It is deliberately constructed only from the explicitly configured Loom
// server/workspace and never opens a Store or starts a local serve process.
type workflowManagementClient struct {
	serverURL string
	workspace string
	doer      workflowHTTPDoer
}

type workflowListAPIResponse struct {
	Workflows []workflowListAPIItem `json:"workflows"`
	Drivers   []workflowListAPIItem `json:"drivers"`
}

type workflowListAPIItem struct {
	Driver          *workflowcatalog.Driver          `json:"driver,omitempty"`
	Version         *workflowcatalog.DriverVersion   `json:"version,omitempty"`
	DriverID        string                           `json:"driver_id,omitempty"`
	Name            string                           `json:"name,omitempty"`
	Status          workflowcatalog.DriverStatus     `json:"status,omitempty"`
	ActiveVersionID string                           `json:"active_version_id,omitempty"`
	Revision        uint64                           `json:"revision,omitempty"`
	BuiltIn         bool                             `json:"built_in,omitempty"`
	Builtin         bool                             `json:"builtin,omitempty"`
	Approved        *bool                            `json:"approved,omitempty"`
	EffectiveTrust  workflowcatalog.DriverTrustLevel `json:"effective_trust,omitempty"`
}

type workflowVersionsAPIResponse struct {
	Driver          *workflowcatalog.Driver          `json:"driver,omitempty"`
	DriverID        string                           `json:"driver_id,omitempty"`
	ActiveVersionID string                           `json:"active_version_id,omitempty"`
	Revision        uint64                           `json:"revision,omitempty"`
	Versions        []*workflowcatalog.DriverVersion `json:"versions"`
}

type workflowVersionActionAPIResponse struct {
	Action  string                         `json:"action"`
	Driver  *workflowcatalog.Driver        `json:"driver"`
	Version *workflowcatalog.DriverVersion `json:"version"`
}

type workflowAuthorVersionRequest struct {
	Files      map[string]string         `json:"files"`
	Entrypoint string                    `json:"entrypoint,omitempty"`
	Runners    []driver.DriverRunnerSpec `json:"runners,omitempty"`
	Manifest   map[string]string         `json:"manifest,omitempty"`
}

type workflowAuthorVersionAPIResponse struct {
	Driver           *workflowcatalog.Driver        `json:"driver"`
	Version          *workflowcatalog.DriverVersion `json:"version"`
	CreatedDriver    bool                           `json:"created_driver"`
	CreatedVersion   bool                           `json:"created_version"`
	ReusedVersion    bool                           `json:"reused_version"`
	Activated        bool                           `json:"activated"`
	BuildDiagnostics string                         `json:"build_diagnostics,omitempty"`
}

type workflowManagementAPIError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Code    string `json:"code"`
	Kind    string `json:"kind"`
}

func newWorkflowManagementClient(_ context.Context) (*workflowManagementClient, error) {
	serverURL := strings.TrimRight(strings.TrimSpace(os.Getenv("LOOM_SERVER_URL")), "/")
	if serverURL == "" {
		return nil, fmt.Errorf("loom workflow management commands require --server or LOOM_SERVER_URL")
	}
	parsed, err := url.Parse(serverURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("invalid Loom management server URL %q", serverURL)
	}
	workspace := strings.TrimSpace(os.Getenv("LOOM_WORKSPACE"))
	if workspace == "" {
		return nil, fmt.Errorf("loom workflow management commands require --workspace or LOOM_WORKSPACE")
	}

	authClient, err := httpclient.New(httpclient.Config{
		ServerURL: serverURL,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	})
	if err != nil {
		return nil, fmt.Errorf("workflow management endpoint discovery: %w", err)
	}
	client := &workflowManagementClient{
		serverURL: serverURL,
		workspace: workspace,
		doer:      authClient,
	}

	return client, nil
}

func (c *workflowManagementClient) listWorkflows(ctx context.Context) (*workflowListAPIResponse, error) {
	var out workflowListAPIResponse
	if err := c.doJSON(ctx, http.MethodGet, c.workspacePath("/workflow-catalog/drivers"), nil, &out); err != nil {
		return nil, err
	}
	if out.Workflows == nil {
		out.Workflows = out.Drivers
	}
	return &out, nil
}

func (c *workflowManagementClient) listVersions(ctx context.Context, workflow string) (*workflowVersionsAPIResponse, error) {
	path := c.workspacePath("/workflows/" + url.PathEscape(strings.TrimSpace(workflow)) + "/versions")
	var out workflowVersionsAPIResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out.Driver == nil {
		out.Driver = &workflowcatalog.Driver{
			WorkspaceKey:    c.workspace,
			DriverID:        out.DriverID,
			ActiveVersionID: out.ActiveVersionID,
			Revision:        out.Revision,
		}
	}
	if out.DriverID == "" {
		out.DriverID = out.Driver.DriverID
	}
	return &out, nil
}

func (c *workflowManagementClient) authorVersion(
	ctx context.Context,
	workflow, requestID string,
	input workflowAuthorVersionRequest,
) (*workflowAuthorVersionAPIResponse, error) {
	path := c.workspacePath("/workflows/" + url.PathEscape(strings.TrimSpace(workflow)) + "/versions")
	var out workflowAuthorVersionAPIResponse
	headers := map[string]string{}
	if requestID = strings.TrimSpace(requestID); requestID != "" {
		headers["Idempotency-Key"] = requestID
	}
	if err := c.doJSONWithHeaders(ctx, http.MethodPost, path, input, &out, headers); err != nil {
		return nil, err
	}
	if out.Driver == nil || out.Version == nil {
		return nil, fmt.Errorf("workflow management API returned an incomplete authoring result")
	}
	return &out, nil
}

func (c *workflowManagementClient) applyVersionAction(ctx context.Context, workflow, versionID, action string) (*workflowVersionActionAPIResponse, error) {
	versions, err := c.listVersions(ctx, workflow)
	if err != nil {
		return nil, fmt.Errorf("resolve workflow driver: %w", err)
	}
	if versions.Driver == nil || strings.TrimSpace(versions.Driver.DriverID) == "" {
		return nil, fmt.Errorf("workflow management API returned no driver for %q", workflow)
	}
	if versions.Driver.Revision == 0 {
		return nil, fmt.Errorf("workflow management API returned no durable revision for driver %q", versions.Driver.DriverID)
	}
	versionID = strings.TrimSpace(versionID)
	found := false
	for _, version := range versions.Versions {
		if version != nil && version.VersionID == versionID {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("workflow version %q: %w", versionID, persistence.ErrNotFound)
	}

	path := c.workspacePath("/workflows/" + url.PathEscape(strings.TrimSpace(workflow)) +
		"/versions/" + url.PathEscape(versionID) + "/" + url.PathEscape(action))
	body := map[string]uint64{"expected_revision": versions.Driver.Revision}
	var out workflowVersionActionAPIResponse
	if err := c.doJSON(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	if out.Driver == nil || out.Version == nil {
		return nil, fmt.Errorf("workflow management API returned an incomplete %s result", action)
	}
	return &out, nil
}

func (c *workflowManagementClient) workspacePath(suffix string) string {
	return "/api/workspaces/" + url.PathEscape(c.workspace) + suffix
}

func (c *workflowManagementClient) doJSON(ctx context.Context, method, path string, input, output any) error {
	return c.doJSONWithHeaders(ctx, method, path, input, output, nil)
}

func (c *workflowManagementClient) doJSONWithHeaders(
	ctx context.Context,
	method, path string,
	input, output any,
	headers map[string]string,
) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode workflow management request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.serverURL+path, body)
	if err != nil {
		return fmt.Errorf("build workflow management request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := c.doer.Do(req)
	if err != nil {
		return fmt.Errorf("workflow management endpoint unavailable at %s: %w", c.serverURL, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, workflowManagementResponseLimit+1))
	if err != nil {
		return fmt.Errorf("read workflow management response: %w", err)
	}
	if len(data) > workflowManagementResponseLimit {
		return fmt.Errorf("workflow management response exceeds %d bytes", workflowManagementResponseLimit)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return workflowManagementStatusError(resp.StatusCode, data)
	}
	if output == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode workflow management response: %w", err)
	}
	return nil
}

func workflowManagementStatusError(status int, data []byte) error {
	var payload workflowManagementAPIError
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
	detail := fmt.Sprintf("workflow management API HTTP %d: %s", status, message)
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
		return errors.New("workflow management API unauthorized: " + detail)
	case http.StatusForbidden:
		return errors.New("workflow management API forbidden: " + detail)
	default:
		return errors.New(detail)
	}
}
