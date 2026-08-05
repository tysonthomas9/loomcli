package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/authmode"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/httpclient"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
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
	bearer    string
}

type workflowListAPIResponse struct {
	Workflows []workflowListAPIItem `json:"workflows"`
	Drivers   []workflowListAPIItem `json:"drivers"`
}

type workflowListAPIItem struct {
	Driver          *domain.Driver          `json:"driver,omitempty"`
	Version         *domain.DriverVersion   `json:"version,omitempty"`
	DriverID        string                  `json:"driver_id,omitempty"`
	Name            string                  `json:"name,omitempty"`
	Status          domain.DriverStatus     `json:"status,omitempty"`
	ActiveVersionID string                  `json:"active_version_id,omitempty"`
	Revision        uint64                  `json:"revision,omitempty"`
	BuiltIn         bool                    `json:"built_in,omitempty"`
	Builtin         bool                    `json:"builtin,omitempty"`
	Approved        *bool                   `json:"approved,omitempty"`
	EffectiveTrust  domain.DriverTrustLevel `json:"effective_trust,omitempty"`
}

type workflowVersionsAPIResponse struct {
	Driver          *domain.Driver          `json:"driver,omitempty"`
	DriverID        string                  `json:"driver_id,omitempty"`
	ActiveVersionID string                  `json:"active_version_id,omitempty"`
	Revision        uint64                  `json:"revision,omitempty"`
	Versions        []*domain.DriverVersion `json:"versions"`
}

type workflowVersionActionAPIResponse struct {
	Action  string                `json:"action"`
	Driver  *domain.Driver        `json:"driver"`
	Version *domain.DriverVersion `json:"version"`
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

	if authClient.AuthMode().Mode == authmode.ModeOpen {
		if err := validateOpenWorkflowManagementEndpoint(parsed); err != nil {
			return nil, err
		}
		credentialDir := filepath.Join(cli.GetWorkspaceRuntimeDir(), ".loom", "operator")
		token, err := authority.ReadLocalOperatorToken(credentialDir)
		if err != nil {
			return nil, fmt.Errorf("workflow management local authentication: %w", err)
		}
		client.bearer = token
	}
	return client, nil
}

// validateOpenWorkflowManagementEndpoint prevents a remote server from
// claiming open mode to make the CLI disclose the durable local operator
// credential. Local/open management is intentionally bound to the same
// explicit loopback-IP shape used by Desktop browser-session launch.
func validateOpenWorkflowManagementEndpoint(parsed *url.URL) error {
	if parsed == nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Host == "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || parsed.Port() == "" {
		return fmt.Errorf("workflow management open-mode endpoint must be an HTTP loopback IP with an explicit port")
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("workflow management open-mode endpoint must use a loopback IP")
	}
	return nil
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
		out.Driver = &domain.Driver{
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
		return nil, fmt.Errorf("workflow version %q: %w", versionID, domain.ErrNotFound)
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
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
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
		return fmt.Errorf("%s: %w", detail, domain.ErrInvalid)
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", detail, domain.ErrNotFound)
	case http.StatusConflict:
		return fmt.Errorf("%s: %w", detail, domain.ErrConflict)
	case http.StatusUnauthorized:
		return errors.New("workflow management API unauthorized: " + detail)
	case http.StatusForbidden:
		return errors.New("workflow management API forbidden: " + detail)
	default:
		return errors.New(detail)
	}
}
