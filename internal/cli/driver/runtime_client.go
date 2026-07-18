package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

const (
	driverRuntimeResponseLimit = 8 << 20
	driverRuntimeHTTPTimeout   = 30 * time.Second

	driverRunIDHeader        = "X-Loom-Driver-Run-Id"
	driverNodeIDHeader       = "X-Loom-Driver-Node-Id"
	driverLeaseIDHeader      = "X-Loom-Driver-Lease-Id"
	driverLeaseTokenHeader   = "X-Loom-Driver-Lease-Token"   // #nosec G101 -- HTTP header name, not a credential.
	driverFencingTokenHeader = "X-Loom-Driver-Fencing-Token" // #nosec G101 -- HTTP header name, not a credential.
)

type driverRuntimeHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// driverRuntimeClient is the hidden driver CLI's transport adapter. It calls
// loom serve's run-scoped API and never opens Store or constructs Execution
// authority locally; the server verifies the run credential and invokes the
// typed Execution command.
type driverRuntimeClient struct {
	apiURL       string
	workspace    string
	runID        string
	nodeID       string
	leaseID      string
	leaseToken   string
	fencingToken int64
	runToken     string
	apiToken     string
	doer         driverRuntimeHTTPDoer
}

type driverRuntimeClientOptions struct {
	WorkspaceKey string
	DriverRunID  string
	NodeID       string
	LeaseID      string
	LeaseToken   string
	FencingToken int64
}

type driverRuntimeAPIError struct {
	Status    int
	Code      string
	Message   string
	Retryable bool
}

func (err *driverRuntimeAPIError) Error() string {
	if err == nil {
		return "driver runtime API error"
	}
	message := strings.TrimSpace(err.Message)
	if message == "" {
		message = http.StatusText(err.Status)
	}
	if err.Code == "" {
		return fmt.Sprintf("driver runtime API HTTP %d: %s", err.Status, message)
	}
	return fmt.Sprintf("driver runtime API HTTP %d (%s): %s", err.Status, err.Code, message)
}

func newDriverRuntimeClient(opts driverRuntimeClientOptions) (*driverRuntimeClient, error) {
	apiURL := strings.TrimRight(firstNonEmpty(os.Getenv("LOOM_DRIVER_API_URL"), os.Getenv("LOOM_TASK_RUN_API_URL")), "/")
	if apiURL == "" {
		return nil, fmt.Errorf("driver runtime commands require LOOM_DRIVER_API_URL")
	}
	parsed, err := url.Parse(apiURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("invalid driver runtime API URL %q", apiURL)
	}
	workspace := firstNonEmpty(opts.WorkspaceKey, os.Getenv("LOOM_DRIVER_WORKSPACE"), os.Getenv("LOOM_WORKER_WORKSPACE"), os.Getenv("LOOM_WORKSPACE"))
	if workspace == "" {
		return nil, fmt.Errorf("driver runtime commands require --workspace-key, LOOM_DRIVER_WORKSPACE, or LOOM_WORKSPACE")
	}
	runToken := strings.TrimSpace(os.Getenv("LOOM_RUN_TOKEN"))
	runID := firstNonEmpty(opts.DriverRunID, os.Getenv("LOOM_DRIVER_RUN_ID"))
	if runToken == "" && runID == "" {
		return nil, fmt.Errorf("driver runtime commands require --driver-run-id or LOOM_DRIVER_RUN_ID when LOOM_RUN_TOKEN is absent")
	}
	var fencingToken int64
	leaseToken := firstNonEmpty(opts.LeaseToken, os.Getenv("LOOM_DRIVER_LEASE_TOKEN"))
	if runToken == "" {
		if leaseToken == "" {
			return nil, fmt.Errorf("legacy driver runtime commands require LOOM_DRIVER_LEASE_TOKEN")
		}
		fencingToken, err = resolveDriverRunFencingToken(opts.FencingToken)
		if err != nil {
			return nil, err
		}
	}
	return &driverRuntimeClient{
		apiURL: apiURL, workspace: workspace, runID: runID,
		nodeID:  firstNonEmpty(opts.NodeID, os.Getenv("LOOM_DRIVER_NODE_ID")),
		leaseID: firstNonEmpty(opts.LeaseID, os.Getenv("LOOM_DRIVER_LEASE_ID")), leaseToken: leaseToken, fencingToken: fencingToken,
		runToken: runToken, apiToken: strings.TrimSpace(os.Getenv("LOOM_DRIVER_API_TOKEN")),
		doer: &http.Client{
			Timeout:       driverRuntimeHTTPTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func (client *driverRuntimeClient) call(ctx context.Context, op string, input, output any) error {
	if client == nil || client.doer == nil {
		return fmt.Errorf("driver runtime client is unavailable: %w", execution.ErrUnavailable)
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode driver runtime op %s: %w", op, err)
	}
	path := client.apiURL + "/api/workspaces/" + url.PathEscape(client.workspace) + "/driver/" + strings.TrimLeft(op, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("build driver runtime op %s: %w", op, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if client.runToken != "" {
		// A run token carries the complete identity. Sending the legacy header
		// quad as well would create an ambiguous-identity downgrade surface.
		req.Header.Set("Authorization", "Bearer "+client.runToken)
	} else {
		req.Header.Set(driverRunIDHeader, client.runID)
		setHeader(req.Header, driverNodeIDHeader, client.nodeID)
		setHeader(req.Header, driverLeaseIDHeader, client.leaseID)
		setHeader(req.Header, driverLeaseTokenHeader, client.leaseToken)
		if client.fencingToken > 0 {
			req.Header.Set(driverFencingTokenHeader, strconv.FormatInt(client.fencingToken, 10))
		}
		if client.apiToken != "" {
			req.Header.Set("Authorization", "Bearer "+client.apiToken)
		}
	}
	response, err := client.doer.Do(req)
	if err != nil {
		return fmt.Errorf("driver runtime endpoint unavailable at %s: %w", client.apiURL, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, driverRuntimeResponseLimit+1))
	if err != nil {
		return fmt.Errorf("read driver runtime op %s response: %w", op, err)
	}
	if len(body) > driverRuntimeResponseLimit {
		return fmt.Errorf("driver runtime op %s response exceeds %d bytes", op, driverRuntimeResponseLimit)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return driverRuntimeStatusError(response.StatusCode, body)
	}
	if output == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, output); err != nil {
		return fmt.Errorf("decode driver runtime op %s response: %w", op, err)
	}
	return nil
}

func (client *driverRuntimeClient) verifyRun(ctx context.Context) (*domain.DriverRun, error) {
	var run domain.DriverRun
	if err := client.call(ctx, "verify-run", struct{}{}, &run); err != nil {
		return nil, fmt.Errorf("verify running driver run: %w", err)
	}
	if strings.TrimSpace(run.RunID) == "" || strings.TrimSpace(run.WorkspaceKey) == "" {
		return nil, fmt.Errorf("verify running driver run returned an empty identity: %w", execution.ErrConflict)
	}
	if run.WorkspaceKey != client.workspace {
		return nil, fmt.Errorf("verify running driver run returned workspace %q, want %q: %w", run.WorkspaceKey, client.workspace, execution.ErrConflict)
	}
	if client.runID != "" && run.RunID != client.runID {
		return nil, fmt.Errorf("verify running driver run returned run %q, want %q: %w", run.RunID, client.runID, execution.ErrConflict)
	}
	return &run, nil
}

func setHeader(headers http.Header, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		headers.Set(key, value)
	}
}

func driverRuntimeStatusError(status int, body []byte) error {
	var envelope struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &envelope)
	apiErr := &driverRuntimeAPIError{
		Status: status, Code: strings.TrimSpace(envelope.Error.Code),
		Message: strings.TrimSpace(envelope.Error.Message), Retryable: envelope.Error.Retryable,
	}
	if apiErr.Message == "" {
		apiErr.Message = strings.TrimSpace(string(body))
	}
	switch apiErr.Code {
	case "invalid":
		return fmt.Errorf("%w: %s", domain.ErrInvalid, apiErr)
	case "not_found":
		return fmt.Errorf("%w: %s", domain.ErrNotFound, apiErr)
	case "not_owner":
		return fmt.Errorf("%w: %s", domain.ErrNotOwner, apiErr)
	case "conflict", "invalid_transition", "unschedulable":
		return fmt.Errorf("%w: %s", domain.ErrConflict, apiErr)
	case "unavailable", "timeout", "canceled":
		return fmt.Errorf("%w: %s", execution.ErrUnavailable, apiErr)
	default:
		return apiErr
	}
}
