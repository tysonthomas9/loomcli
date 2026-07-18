package data

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

const (
	taskRunDataResponseLimit = 8 << 20
	taskRunDataHTTPTimeout   = 30 * time.Second

	envTaskRunAPIURL       = "LOOM_TASK_RUN_API_URL"
	envTaskRunID           = "LOOM_TASK_RUN_ID"
	envTaskID              = "LOOM_TASK_ID"
	envTaskRunNodeID       = "LOOM_TASK_RUN_NODE_ID"
	envTaskRunLeaseID      = "LOOM_TASK_RUN_LEASE_ID"
	envTaskRunLeaseToken   = "LOOM_TASK_RUN_LEASE_TOKEN"   // #nosec G101 -- environment variable name, not a credential.
	envRunnerLeaseToken    = "LOOM_RUNNER_LEASE_TOKEN"     // #nosec G101 -- legacy environment variable name, not a credential.
	envTaskRunFencingToken = "LOOM_TASK_RUN_FENCING_TOKEN" // #nosec G101 -- environment variable name, not a credential.
	envDriverWorkspace     = "LOOM_DRIVER_WORKSPACE"

	taskRunIDHeader           = "X-Loom-Task-Run-Id"
	taskRunNodeIDHeader       = "X-Loom-Task-Run-Node-Id"
	taskRunLeaseIDHeader      = "X-Loom-Task-Run-Lease-Id"
	taskRunFencingTokenHeader = "X-Loom-Task-Run-Fencing-Token" // #nosec G101 -- HTTP header name, not a credential.
)

type taskRunDataHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// taskRunDataClient is the narrowly scoped transport used by `loom data show`
// and design-only `loom data update` inside a TaskRun. It carries only the
// lease tuple already provisioned to the runner and never opens Store or reads
// Fleet credentials.
type taskRunDataClient struct {
	apiURL       string
	workspace    string
	taskRunID    string
	taskID       string
	nodeID       string
	leaseID      string
	leaseToken   string
	fencingToken int64
	doer         taskRunDataHTTPDoer
}

type taskRunDataIdentity struct {
	taskRunID string
	taskID    string
	nodeID    string
	leaseID   string
}

type taskRunDataConfig struct {
	apiURL       string
	workspace    string
	identity     taskRunDataIdentity
	leaseToken   string
	fencingToken int64
}

type taskRunDataAPIError struct {
	status    int
	code      string
	message   string
	retryable bool
}

var taskRunDataMarkerEnv = [...]string{
	envTaskRunAPIURL,
	envTaskRunID,
	envTaskRunNodeID,
	envTaskRunLeaseID,
	envTaskRunLeaseToken,
	envRunnerLeaseToken,
	envTaskRunFencingToken,
}

// EnforceTaskRunCommandScope prevents the model-controlled TaskRun process
// from escaping the lease-authenticated task facade through a sibling
// `loom data` command. The CLI root invokes this before resolving or building
// any generic issue backend; show and design-only update perform their
// narrower per-command validation after this subtree gate.
func EnforceTaskRunCommandScope(cmd *cobra.Command) error {
	if cmd == nil || !taskRunDataModeActive() || !belongsToDataCommandTree(cmd) {
		return nil
	}
	if cmd == showCmd || cmd == updateCmd {
		return nil
	}
	return fmt.Errorf("task-run data mode only permits `loom data show` and design-only `loom data update`; rejected %q", cmd.CommandPath())
}

func taskRunDataModeActive() bool {
	for _, name := range taskRunDataMarkerEnv {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	return false
}

func belongsToDataCommandTree(cmd *cobra.Command) bool {
	for current := cmd; current != nil; current = current.Parent() {
		if current == dataRootCmd {
			return true
		}
	}
	return false
}

func (err *taskRunDataAPIError) Error() string {
	if err == nil {
		return "task-run data API error"
	}
	message := strings.TrimSpace(err.message)
	if message == "" {
		message = http.StatusText(err.status)
	}
	if err.code == "" {
		return fmt.Sprintf("task-run data API HTTP %d: %s", err.status, message)
	}
	return fmt.Sprintf("task-run data API HTTP %d (%s): %s", err.status, err.code, message)
}

// taskRunDataClientFromEnv returns active=false only outside a TaskRun. Once
// any TaskRun-specific marker is present, incomplete configuration fails
// closed instead of falling back to a direct issue backend.
func taskRunDataClientFromEnv() (*taskRunDataClient, bool, error) {
	if !taskRunDataModeActive() {
		return nil, false, nil
	}

	config, err := taskRunDataConfigFromEnv()
	if err != nil {
		return nil, true, err
	}
	return &taskRunDataClient{
		apiURL:       strings.TrimRight(config.apiURL, "/"),
		workspace:    config.workspace,
		taskRunID:    config.identity.taskRunID,
		taskID:       config.identity.taskID,
		nodeID:       config.identity.nodeID,
		leaseID:      config.identity.leaseID,
		leaseToken:   config.leaseToken,
		fencingToken: config.fencingToken,
		doer: &http.Client{
			Timeout:       taskRunDataHTTPTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, true, nil
}

func taskRunDataConfigFromEnv() (*taskRunDataConfig, error) {
	apiURL, err := taskRunDataAPIURLFromEnv()
	if err != nil {
		return nil, err
	}
	workspace, err := taskRunDataWorkspaceFromEnv()
	if err != nil {
		return nil, err
	}
	identity, err := taskRunDataIdentityFromEnv()
	if err != nil {
		return nil, err
	}
	leaseToken, err := taskRunDataLeaseTokenFromEnv()
	if err != nil {
		return nil, err
	}
	fencingToken, err := taskRunDataFencingTokenFromEnv()
	if err != nil {
		return nil, err
	}
	return &taskRunDataConfig{
		apiURL:       apiURL,
		workspace:    workspace,
		identity:     identity,
		leaseToken:   leaseToken,
		fencingToken: fencingToken,
	}, nil
}

func taskRunDataAPIURLFromEnv() (string, error) {
	apiURL, err := requireTaskRunDataEnv(envTaskRunAPIURL)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(apiURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid %s", envTaskRunAPIURL)
	}
	return apiURL, nil
}

func taskRunDataWorkspaceFromEnv() (string, error) {
	workspace := strings.TrimSpace(os.Getenv(envDriverWorkspace))
	if workspace == "" {
		workspace = strings.TrimSpace(os.Getenv("LOOM_WORKSPACE"))
	}
	if workspace == "" {
		return "", fmt.Errorf("task-run data commands require %s or LOOM_WORKSPACE", envDriverWorkspace)
	}
	return workspace, nil
}

func taskRunDataIdentityFromEnv() (taskRunDataIdentity, error) {
	taskRunID, err := requireTaskRunDataEnv(envTaskRunID)
	if err != nil {
		return taskRunDataIdentity{}, err
	}
	taskID, err := requireTaskRunDataEnv(envTaskID)
	if err != nil {
		return taskRunDataIdentity{}, err
	}
	nodeID, err := requireTaskRunDataEnv(envTaskRunNodeID)
	if err != nil {
		return taskRunDataIdentity{}, err
	}
	leaseID, err := requireTaskRunDataEnv(envTaskRunLeaseID)
	if err != nil {
		return taskRunDataIdentity{}, err
	}
	return taskRunDataIdentity{taskRunID: taskRunID, taskID: taskID, nodeID: nodeID, leaseID: leaseID}, nil
}

func taskRunDataLeaseTokenFromEnv() (string, error) {
	leaseToken := strings.TrimSpace(os.Getenv(envTaskRunLeaseToken))
	if leaseToken == "" {
		leaseToken = strings.TrimSpace(os.Getenv(envRunnerLeaseToken))
	}
	if leaseToken == "" {
		return "", fmt.Errorf("task-run data commands require %s", envTaskRunLeaseToken)
	}
	return leaseToken, nil
}

func taskRunDataFencingTokenFromEnv() (int64, error) {
	rawFence, err := requireTaskRunDataEnv(envTaskRunFencingToken)
	if err != nil {
		return 0, err
	}
	fence, err := strconv.ParseInt(rawFence, 10, 64)
	if err != nil || fence <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", envTaskRunFencingToken)
	}
	return fence, nil
}

func requireTaskRunDataEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("task-run data commands require %s", name)
	}
	return value, nil
}

func (client *taskRunDataClient) call(ctx context.Context, op string, input, output any) error {
	if client == nil || client.doer == nil {
		return fmt.Errorf("task-run data API is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode task-run data op %s: %w", op, err)
	}
	endpoint := client.apiURL + "/api/workspaces/" + url.PathEscape(client.workspace) + "/task-run/" + strings.TrimLeft(op, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("build task-run data op %s: %w", op, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+client.leaseToken)
	req.Header.Set(taskRunIDHeader, client.taskRunID)
	req.Header.Set(taskRunNodeIDHeader, client.nodeID)
	req.Header.Set(taskRunLeaseIDHeader, client.leaseID)
	req.Header.Set(taskRunFencingTokenHeader, strconv.FormatInt(client.fencingToken, 10))

	response, err := client.doer.Do(req)
	if err != nil {
		return fmt.Errorf("task-run data endpoint unavailable: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, taskRunDataResponseLimit+1))
	if err != nil {
		return fmt.Errorf("read task-run data op %s response: %w", op, err)
	}
	if len(body) > taskRunDataResponseLimit {
		return fmt.Errorf("task-run data op %s response exceeds %d bytes", op, taskRunDataResponseLimit)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return taskRunDataStatusError(response.StatusCode, body)
	}
	if output == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, output); err != nil {
		return fmt.Errorf("decode task-run data op %s response: %w", op, err)
	}
	return nil
}

func taskRunDataStatusError(status int, body []byte) error {
	var envelope struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &envelope)
	return &taskRunDataAPIError{
		status: status, code: envelope.Error.Code,
		message: envelope.Error.Message, retryable: envelope.Error.Retryable,
	}
}

func (client *taskRunDataClient) requireBoundTask(requested string) error {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return fmt.Errorf("task ID is required")
	}
	if requested != client.taskID {
		return fmt.Errorf("task-run data access is restricted to %s; requested %s", client.taskID, requested)
	}
	return nil
}

func (client *taskRunDataClient) getTask(ctx context.Context, taskID string) (*backend.IssueDetailData, error) {
	if err := client.requireBoundTask(taskID); err != nil {
		return nil, err
	}
	var result struct {
		Task *backend.IssueDetailData `json:"task"`
	}
	if err := client.call(ctx, "task-get", map[string]string{"taskId": client.taskID}, &result); err != nil {
		return nil, err
	}
	if result.Task == nil {
		return nil, fmt.Errorf("task-run data API returned no task")
	}
	if strings.TrimSpace(result.Task.ID) != client.taskID {
		return nil, fmt.Errorf("task-run data API returned a task outside the active run")
	}
	return result.Task, nil
}

func (client *taskRunDataClient) updateDesign(ctx context.Context, taskID, design string, designFormat *string) error {
	if err := client.requireBoundTask(taskID); err != nil {
		return err
	}
	format := "markdown"
	if designFormat != nil && strings.TrimSpace(*designFormat) != "" {
		format = strings.TrimSpace(*designFormat)
	}
	requestID := newTaskRunDataRequestID(client.taskRunID, format, design)
	input := struct {
		RequestID    string `json:"requestId"`
		Design       string `json:"design"`
		DesignFormat string `json:"designFormat"`
	}{RequestID: requestID, Design: design, DesignFormat: format}
	return client.call(ctx, "task-design-update", input, nil)
}

// newTaskRunDataRequestID gives exact planner retries one durable command
// identity while changed content or format produces a different command. The
// NUL separators make the three exact byte domains unambiguous.
func newTaskRunDataRequestID(taskRunID, format, design string) string {
	digest := sha256.Sum256([]byte(taskRunID + "\x00" + format + "\x00" + design))
	return "task-run-design-update:" + hex.EncodeToString(digest[:])
}
