package managementapi

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
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const responseLimit = 8 << 20

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client is the shared standalone-CLI adapter for authenticated Loom
// management routes. It never opens Store or constructs capability authority
// locally; open-mode loopback servers receive the durable local operator
// credential, while externally authenticated clients retain their configured
// HTTP transport.
type Client struct {
	serverURL string
	workspace string
	doer      httpDoer
	bearer    string
}

type SubmitDriverRunRequest struct {
	CLICommand      string          `json:"cli_command"`
	DriverRef       string          `json:"driver_ref"`
	DriverVersionID string          `json:"driver_version_id,omitempty"`
	RunID           string          `json:"run_id,omitempty"`
	IdempotencyKey  string          `json:"idempotency_key,omitempty"`
	Entrypoint      string          `json:"entrypoint,omitempty"`
	EpicID          string          `json:"epic_id,omitempty"`
	Payload         json.RawMessage `json:"payload"`
}

func New(_ context.Context, purpose string) (*Client, error) {
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		purpose = "Loom management command"
	}
	serverURL := strings.TrimRight(strings.TrimSpace(os.Getenv("LOOM_SERVER_URL")), "/")
	if serverURL == "" {
		return nil, fmt.Errorf("%s requires --server or LOOM_SERVER_URL", purpose)
	}
	parsed, err := url.Parse(serverURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("invalid Loom management server URL %q", serverURL)
	}
	workspace := strings.TrimSpace(os.Getenv("LOOM_WORKSPACE"))
	if workspace == "" {
		return nil, fmt.Errorf("%s requires --workspace or LOOM_WORKSPACE", purpose)
	}
	authClient, err := httpclient.New(httpclient.Config{
		ServerURL: serverURL,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	})
	if err != nil {
		return nil, fmt.Errorf("%s endpoint discovery: %w", purpose, err)
	}
	client := &Client{serverURL: serverURL, workspace: workspace, doer: authClient}
	if authClient.AuthMode().Mode == authmode.ModeOpen {
		if err := validateOpenEndpoint(parsed); err != nil {
			return nil, err
		}
		credentialDir := filepath.Join(cli.GetWorkspaceRuntimeDir(), ".loom", "operator")
		token, err := authority.ReadLocalOperatorToken(credentialDir)
		if err != nil {
			return nil, fmt.Errorf("%s local authentication: %w", purpose, err)
		}
		client.bearer = token
	}
	return client, nil
}

func validateOpenEndpoint(parsed *url.URL) error {
	if parsed == nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Host == "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || parsed.Port() == "" {
		return fmt.Errorf("loom management open-mode endpoint must be an HTTP loopback IP with an explicit port")
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("loom management open-mode endpoint must use a loopback IP")
	}
	return nil
}

func (client *Client) Workspace() string {
	if client == nil {
		return ""
	}
	return client.workspace
}

func (client *Client) workspacePath(suffix string) string {
	return "/api/workspaces/" + url.PathEscape(client.workspace) + suffix
}

func (client *Client) SubmitDriverRun(ctx context.Context, request SubmitDriverRunRequest) (*domain.DriverRun, error) {
	var run domain.DriverRun
	if err := client.doJSON(ctx, http.MethodPost, client.workspacePath("/execution/driver-runs"), request, &run); err != nil {
		return nil, err
	}
	if strings.TrimSpace(run.RunID) == "" {
		return nil, errors.New("loom management API returned no DriverRun")
	}
	return &run, nil
}

func (client *Client) GetDriverRun(ctx context.Context, runID string) (*domain.DriverRun, error) {
	var run domain.DriverRun
	if err := client.doJSON(ctx, http.MethodGet, client.workspacePath("/runs/"+url.PathEscape(strings.TrimSpace(runID))), nil, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

func (client *Client) CreateWorkerProfile(ctx context.Context, command execution.CreateWorkerProfileCommand) (*execution.WorkerProfile, error) {
	profileID := strings.TrimSpace(command.ProfileID)
	if profileID == "" {
		return nil, fmt.Errorf("worker profile id is required: %w", domain.ErrInvalid)
	}
	command.ProfileID = profileID
	var profile execution.WorkerProfile
	if err := client.doJSON(ctx, http.MethodPost, client.workspacePath("/execution/worker-profiles"), command, &profile); err != nil {
		return nil, err
	}
	if profile.ProfileID != profileID || profile.WorkspaceKey != client.workspace {
		return nil, errors.New("loom management API returned an invalid WorkerProfile identity")
	}
	return &profile, nil
}

func (client *Client) UpdateWorkerProfile(ctx context.Context, profileID string, patch execution.WorkerProfilePatch) (*execution.WorkerProfile, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return nil, fmt.Errorf("worker profile id is required: %w", domain.ErrInvalid)
	}
	var profile execution.WorkerProfile
	if err := client.doJSON(ctx, http.MethodPatch, client.workspacePath("/execution/worker-profiles/"+url.PathEscape(profileID)), patch, &profile); err != nil {
		return nil, err
	}
	if profile.ProfileID != profileID || profile.WorkspaceKey != client.workspace {
		return nil, errors.New("loom management API returned an invalid WorkerProfile identity")
	}
	return &profile, nil
}

func (client *Client) DeleteWorkerProfile(ctx context.Context, profileID string) error {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return fmt.Errorf("worker profile id is required: %w", domain.ErrInvalid)
	}
	return client.doJSON(ctx, http.MethodDelete, client.workspacePath("/execution/worker-profiles/"+url.PathEscape(profileID)), nil, nil)
}

func (client *Client) doJSON(ctx context.Context, method, path string, input, output any) error {
	if client == nil || client.doer == nil {
		return errors.New("loom management client is unavailable")
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode Loom management request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, client.serverURL+path, body)
	if err != nil {
		return fmt.Errorf("build Loom management request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if client.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+client.bearer)
	}
	response, err := client.doer.Do(req)
	if err != nil {
		return fmt.Errorf("loom management endpoint unavailable at %s: %w", client.serverURL, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return fmt.Errorf("read Loom management response: %w", err)
	}
	if len(data) > responseLimit {
		return fmt.Errorf("loom management response exceeds %d bytes", responseLimit)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return statusError(response.StatusCode, data)
	}
	if output == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode Loom management response: %w", err)
	}
	return nil
}

func statusError(status int, data []byte) error {
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
		Code    string `json:"code"`
	}
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
	detail := fmt.Sprintf("Loom management API HTTP %d: %s", status, message)
	switch status {
	case http.StatusBadRequest, http.StatusPreconditionRequired, http.StatusPreconditionFailed:
		return fmt.Errorf("%s: %w", detail, domain.ErrInvalid)
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", detail, domain.ErrNotFound)
	case http.StatusConflict:
		return fmt.Errorf("%s: %w", detail, domain.ErrConflict)
	case http.StatusUnauthorized:
		return errors.New("Loom management API unauthorized: " + detail)
	case http.StatusForbidden:
		return errors.New("Loom management API forbidden: " + detail)
	default:
		return errors.New(detail)
	}
}
