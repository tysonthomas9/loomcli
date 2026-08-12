// Package taskrunapi serves the task-runner HTTP API on loom serve: the
// task-run operations sdk/runner.js (TaskRunClient) used to perform against
// fleet-db directly (logs.append, heartbeat, completeRun, getTaskRun,
// artifacts declare/get/list/upload/finalize), re-hosted behind serve so task
// harnesses — which execute model-generated code — never hold fleet-db
// credentials (§9.1: only loom serve touches fleet-db; closes the runner leg
// of vet A4).
//
// Surface shape mirrors driverapi (SDK v2 transport): camelCase JSON on the
// wire, structured errors {code, message, retryable}.
//
// Authentication is per-task-run lease credentials, the same material
// fleet-db's own fenced task-run checks verify today:
//
//   - Authorization: Bearer <lease token> (LOOM_TASK_RUN_LEASE_TOKEN, minted
//     at claim and injected by the task bridge)
//   - X-Loom-Task-Run-Id / -Node-Id / -Lease-Id / -Fencing-Token identity
//     headers (mirrors of the LOOM_TASK_RUN_* env vars)
//
// Fenced TaskRun mutations (heartbeat, log-append, complete) and the Artifacts
// capability both forward the opaque lease token to FleetDB's owner-fenced
// command/query ports, so lease-token-hash + {node, lease, fencing} remains the
// durable authority. Legacy get/task-get operations still prove ownership with
// a fenced no-op heartbeat before reading their compatibility projections.
package taskrunapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	serverhandler "github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// maxTaskRunOpBodyBytes caps inbound task-run op payloads (matches the
// driver-op cap; artifact content uses its own route and cap).
const maxTaskRunOpBodyBytes = 8 << 20

// maxArtifactContentBytes caps raw artifact content uploads.
const maxArtifactContentBytes = 64 << 20

// Task-run identity headers. Mirrors of the LOOM_TASK_RUN_* env vars the
// task bridge injects into runner processes.
const (
	HeaderTaskRunID           = "X-Loom-Task-Run-Id"
	HeaderTaskRunNodeID       = "X-Loom-Task-Run-Node-Id"
	HeaderTaskRunLeaseID      = "X-Loom-Task-Run-Lease-Id"
	HeaderTaskRunFencingToken = "X-Loom-Task-Run-Fencing-Token" //nolint:gosec // header name, not a credential
)

// taskRunOwnerType is the artifact owner type this surface is scoped to.
const taskRunOwnerType = "task_run"

// IssueBackendFactory builds a workspace-scoped fleet-db issue backend acting
// as the given actor. Overridable in tests.
type IssueBackendFactory func(ws, actor string) (backend.IssueBackend, error)

// Config wires the module's dependencies.
type Config struct {
	Store taskRunProjectionStore
	// Execution owns every running TaskRun mutation. Store is a read-only
	// projection dependency for exact TaskRun lookup and lease verification.
	Execution   execution.TaskRunAPI
	Authorities execution.TaskRunAuthorityResolver
	// Artifacts is the owner-fenced capability API. A nil API fails artifact
	// operations closed.
	Artifacts artifactsmodule.API
	// DaytonaProvider is the host-owned opaque provider broker. It is reachable
	// only after this module verifies the exact Daytona TaskRun lease/fence.
	DaytonaProvider execution.DaytonaProviderBroker
	// FleetBaseURL is the fleet-db HTTP base URL used to build issue
	// backends for exact-task reads. TaskRun mutations use Execution ports.
	FleetBaseURL string
	// IssueBackends overrides the default fleet-db issue backend factory.
	IssueBackends IssueBackendFactory
}

type taskRunProjectionStore interface {
	TaskRuns() store.TaskRunStore
}

// Module serves the workspace-scoped task-run routes.
type Module struct {
	store           taskRunProjectionStore
	execution       execution.TaskRunAPI
	authorities     execution.TaskRunAuthorityResolver
	artifacts       artifactsmodule.API
	daytonaProvider execution.DaytonaProviderBroker
	issueBackends   IssueBackendFactory
	ops             map[string]opHandler
	now             func() time.Time
}

// NewModule constructs the task-run API module. Nil-safe: with a nil store,
// Register registers nothing.
func NewModule(cfg Config) *Module {
	m := &Module{
		store:           cfg.Store,
		execution:       cfg.Execution,
		authorities:     cfg.Authorities,
		artifacts:       cfg.Artifacts,
		daytonaProvider: cfg.DaytonaProvider,
		issueBackends:   cfg.IssueBackends,
		now:             func() time.Time { return time.Now().UTC() },
	}
	m.ops = map[string]opHandler{
		"get":                m.get,
		"task-get":           m.taskGet,
		"task-design-update": m.taskDesignUpdate,
		"heartbeat":          m.heartbeat,
		"log-append":         m.logAppend,
		"complete":           m.complete,
		"daytona-execute":    m.daytonaExecute,
		"artifact-declare":   m.artifactDeclare,
		"artifact-get":       m.artifactGet,
		"artifact-list":      m.artifactList,
		"artifact-finalize":  m.artifactFinalize,
	}
	if m.issueBackends == nil {
		m.issueBackends = defaultIssueBackends(cfg.FleetBaseURL)
	}
	return m
}

// defaultIssueBackends builds the production issue-backend factory: a
// fleet-db client per (workspace, actor) against the configured base URL.
// The fleet-db API key stays inside the serve process.
func defaultIssueBackends(baseURL string) IssueBackendFactory {
	return func(ws, actor string) (backend.IssueBackend, error) {
		issueBackend, err := fleet.New(fleet.Config{
			BaseURL:     baseURL,
			WorkspaceID: ws,
			APIKey:      os.Getenv(bootstrap.EnvFleetDBAPIKey),
			Actor:       actor,
		})
		if err != nil {
			return nil, fmt.Errorf("create fleet-db issue backend: %w", err)
		}
		return issueBackend, nil
	}
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.store == nil || m.execution == nil || m.authorities == nil {
		return
	}
	mux.HandleFunc("POST /api/workspaces/{ws}/task-run/{op}", m.handleOp)
	// Raw artifact content upload: the body is the content itself, so it
	// cannot ride the JSON {op} route.
	mux.HandleFunc("PUT /api/workspaces/{ws}/task-run/artifacts/{artifactId}/content", m.handleArtifactContent)
}

// leaseIdentity is the per-request task-run lease identity: the same
// {taskRunId, nodeId, leaseId, leaseToken, fencingToken} tuple fleet-db's
// fenced task-run checks verify.
type leaseIdentity struct {
	TaskRunID    string
	NodeID       string
	LeaseID      string
	LeaseToken   string
	FencingToken int64
}

type opHandler func(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error)

func (m *Module) handleOp(w http.ResponseWriter, r *http.Request) {
	id, ok := authenticate(w, r)
	if !ok {
		return
	}
	op := strings.TrimSpace(r.PathValue("op"))
	handler, ok := m.ops[op]
	if !ok {
		writeOpErrorDetails(w, http.StatusNotFound, "unknown_op", fmt.Sprintf("unknown task-run op %q", op), false, map[string]any{"op": op})
		return
	}
	body, err := readOpBody(w, r)
	if err != nil {
		writeOpError(w, http.StatusBadRequest, "invalid", "read task-run op payload: "+err.Error(), false)
		return
	}
	result, err := handler(r.Context(), r.PathValue("ws"), id, body)
	if err != nil {
		writeDomainOpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func readOpBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	body, err := readAll(w, r, maxTaskRunOpBodyBytes)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte("{}")
	}
	return body, nil
}

// authenticate resolves the request's lease identity. The lease token is the
// only credential; the identity headers tell the store which fenced row to
// verify it against. On failure the structured error has been written.
func authenticate(w http.ResponseWriter, r *http.Request) (leaseIdentity, bool) {
	token := bearerCredential(r)
	if token == "" {
		writeOpError(w, http.StatusUnauthorized, "unauthenticated", "Authorization: Bearer <task-run lease token> required", false)
		return leaseIdentity{}, false
	}
	id := leaseIdentity{
		TaskRunID:  strings.TrimSpace(r.Header.Get(HeaderTaskRunID)),
		NodeID:     strings.TrimSpace(r.Header.Get(HeaderTaskRunNodeID)),
		LeaseID:    strings.TrimSpace(r.Header.Get(HeaderTaskRunLeaseID)),
		LeaseToken: token,
	}
	if id.TaskRunID == "" {
		writeOpError(w, http.StatusUnauthorized, "unauthenticated", HeaderTaskRunID+" header required", false)
		return leaseIdentity{}, false
	}
	rawFence := strings.TrimSpace(r.Header.Get(HeaderTaskRunFencingToken))
	fence, err := strconv.ParseInt(rawFence, 10, 64)
	if err != nil || fence <= 0 {
		writeOpError(w, http.StatusUnauthorized, "unauthenticated", HeaderTaskRunFencingToken+" header must be a positive integer", false)
		return leaseIdentity{}, false
	}
	id.FencingToken = fence
	return id, true
}

// bearerCredential extracts the Bearer credential, "" when absent.
func bearerCredential(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	token, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(token)
}

// errLeaseDenied marks ownership-verification failures so they map to 401
// (the credential is bad/superseded) instead of the generic 403 a fenced
// pass-through op reports.
var errLeaseDenied = errors.New("task-run lease verification failed")

// verifyLease proves the caller owns the running task run by issuing a
// fenced no-op heartbeat through the store: fleet-db checks the lease token
// hash and the {node, lease, fencing} tuple — the exact validation the
// runner's direct fleet-db calls were subject to. The heartbeat doubles as
// liveness (it only touches last_heartbeat; empty refs/metadata are no-ops).
func (m *Module) verifyLease(ctx context.Context, ws string, id leaseIdentity) (*domain.TaskRun, error) {
	owner, auth, err := m.taskRunAuthority(ctx, ws, execution.ActionHeartbeat, id)
	if err == nil {
		_, err = m.execution.Heartbeat(ctx, auth, execution.HeartbeatCommand{
			WorkspaceKey: ws,
			Owner:        owner,
			At:           m.now(),
		})
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errLeaseDenied, err)
	}
	run, err := m.store.TaskRuns().Get(ctx, ws, id.TaskRunID)
	if err != nil {
		return nil, fmt.Errorf("load verified task run: %w", err)
	}
	return run, nil
}

func (m *Module) taskRunAuthority(
	ctx context.Context,
	ws string,
	action authority.Action,
	id leaseIdentity,
) (execution.Owner, authority.ExecutionAuthority, error) {
	owner := execution.Owner{
		ResourceKind: execution.ResourceTaskRun,
		ResourceID:   id.TaskRunID,
		NodeID:       id.NodeID,
		LeaseID:      id.LeaseID,
		LeaseToken:   id.LeaseToken,
		FencingToken: id.FencingToken,
	}
	auth, err := m.authorities.ResolveTaskRunAuthority(ctx, ws, action, owner)
	if err != nil {
		return execution.Owner{}, authority.ExecutionAuthority{}, err
	}
	return owner, auth, nil
}

func decodeParams[T any](body []byte) (T, error) {
	var params T
	err := serverhandler.DecodeOneJSONBytes(body, &params, serverhandler.JSONDecodeOptions{
		MaxBytes: maxTaskRunOpBodyBytes,
	})
	if errors.Is(err, serverhandler.ErrTrailingJSON) {
		err = errors.New("multiple JSON values")
	}
	if err != nil {
		return params, fmt.Errorf("decode task-run op params: %s: %w", err.Error(), domain.ErrInvalid)
	}
	return params, nil
}

// decodeStrictParams is reserved for operations that cross from
// model-controlled task harnesses into Work Item projections or commands.
// Unknown fields fail closed so the TaskRun surface cannot widen implicitly.
func decodeStrictParams[T any](body []byte) (T, error) {
	var params T
	err := serverhandler.DecodeOneJSONBytes(body, &params, serverhandler.JSONDecodeOptions{
		MaxBytes:              maxTaskRunOpBodyBytes,
		DisallowUnknownFields: true,
	})
	if errors.Is(err, serverhandler.ErrTrailingJSON) {
		err = errors.New("multiple JSON values")
	}
	if err != nil {
		return params, fmt.Errorf("decode task-run op params: %s: %w", err.Error(), domain.ErrInvalid)
	}
	return params, nil
}

func (m *Module) get(ctx context.Context, ws string, id leaseIdentity, _ []byte) (any, error) {
	run, err := m.verifyLease(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	return driverpkg.TaskRunResultFromDomain(run), nil
}

func (m *Module) taskGet(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error) {
	params, err := decodeStrictParams[struct {
		TaskID string `json:"taskId"`
	}](body)
	if err != nil {
		return nil, err
	}
	run, err := m.verifyLease(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"taskRun": driverpkg.TaskRunResultFromDomain(run)}
	taskID := strings.TrimSpace(run.TaskID)
	if taskID == "" {
		return result, nil
	}
	if requested := strings.TrimSpace(params.TaskID); requested != "" && requested != taskID {
		return nil, fmt.Errorf("task %q is outside task run %q: %w", requested, run.TaskRunID, domain.ErrNotOwner)
	}
	issueBackend, err := m.issueBackends(ws, taskRunActor(run))
	if err != nil {
		return nil, err
	}
	task, err := issueBackend.Get(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	result["task"] = task
	return result, nil
}

type taskDesignUpdateParams struct {
	RequestID    string  `json:"requestId"`
	Design       *string `json:"design"`
	DesignFormat *string `json:"designFormat,omitempty"`
}

// taskDesignUpdate is the deliberately narrow Execution port used by
// `loom data update --design [--design-format]` inside a TaskRun. FleetDB's
// atomic command verifies the owner and derives the Work Item from TaskRun;
// callers cannot choose another Work Item or smuggle a generic issue field.
func (m *Module) taskDesignUpdate(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error) {
	params, err := decodeStrictParams[taskDesignUpdateParams](body)
	if err != nil {
		return nil, err
	}
	requestID := strings.TrimSpace(params.RequestID)
	if requestID == "" {
		return nil, fmt.Errorf("requestId is required: %w", domain.ErrInvalid)
	}
	if params.Design == nil || strings.TrimSpace(*params.Design) == "" {
		return nil, fmt.Errorf("nonblank design is required: %w", domain.ErrInvalid)
	}
	format := "markdown"
	if params.DesignFormat != nil && strings.TrimSpace(*params.DesignFormat) != "" {
		format = strings.TrimSpace(*params.DesignFormat)
	}
	if format != "markdown" && format != "html" {
		return nil, fmt.Errorf("designFormat must be markdown or html: %w", domain.ErrInvalid)
	}
	params.DesignFormat = &format

	owner, auth, err := m.taskRunAuthority(ctx, ws, execution.ActionUpdateTaskRunWorkItemDesign, id)
	if err != nil {
		return nil, fmt.Errorf("authorize task design update: %w", err)
	}
	result, err := m.execution.UpdateWorkItemDesign(ctx, auth, execution.UpdateTaskRunWorkItemDesignCommand{
		WorkspaceKey: ws,
		RequestID:    requestID,
		Owner:        owner,
		Design:       params.Design,
		DesignFormat: params.DesignFormat,
	})
	if err != nil {
		return nil, fmt.Errorf("update task design: %w", err)
	}
	return map[string]any{"taskId": result.WorkItemID, "actionId": result.ActionID, "replayed": result.Replay}, nil
}

// taskRunActor is the audit actor for exact-task Work Item operations performed
// on behalf of a task runner.
func taskRunActor(run *domain.TaskRun) string {
	if run.DriverRunID != "" {
		return driverpkg.DriverRunActor(run.DriverRunID)
	}
	return "task-run:" + run.TaskRunID
}

func (m *Module) heartbeat(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		RuntimeMetadata map[string]string `json:"runtimeMetadata"`
		LogsRef         string            `json:"logsRef"`
		ArtifactsRef    string            `json:"artifactsRef"`
	}](body)
	if err != nil {
		return nil, err
	}
	owner, auth, err := m.taskRunAuthority(ctx, ws, execution.ActionHeartbeat, id)
	if err != nil {
		return nil, fmt.Errorf("authorize task run heartbeat: %w", err)
	}
	_, err = m.execution.Heartbeat(ctx, auth, execution.HeartbeatCommand{
		WorkspaceKey:    ws,
		Owner:           owner,
		At:              m.now(),
		RuntimeMetadata: params.RuntimeMetadata,
		LogsRef:         params.LogsRef,
		ArtifactsRef:    params.ArtifactsRef,
	})
	if err != nil {
		return nil, fmt.Errorf("heartbeat task run: %w", err)
	}
	run, err := m.store.TaskRuns().Get(ctx, ws, id.TaskRunID)
	if err != nil {
		return nil, fmt.Errorf("load heartbeat task run: %w", err)
	}
	return driverpkg.TaskRunResultFromDomain(run), nil
}

type logAppendParams struct {
	RequestID      string     `json:"requestId"`
	RequestIDSnake string     `json:"request_id"`
	Stream         string     `json:"stream"`
	Text           *string    `json:"text"`
	Timestamp      *time.Time `json:"timestamp"`
}

func (params logAppendParams) replayIdentity() (string, time.Time, error) {
	requestID := strings.TrimSpace(params.RequestID)
	snakeRequestID := strings.TrimSpace(params.RequestIDSnake)
	if requestID != "" && snakeRequestID != "" && requestID != snakeRequestID {
		return "", time.Time{}, fmt.Errorf("requestId and request_id disagree: %w", domain.ErrInvalid)
	}
	if requestID == "" {
		requestID = snakeRequestID
	}
	if requestID == "" || params.Timestamp == nil || params.Timestamp.IsZero() {
		return "", time.Time{}, fmt.Errorf("requestId and timestamp required: %w", domain.ErrInvalid)
	}
	return requestID, params.Timestamp.UTC(), nil
}

func (m *Module) logAppend(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error) {
	params, err := decodeParams[logAppendParams](body)
	if err != nil {
		return nil, err
	}
	if params.Text == nil {
		return nil, fmt.Errorf("text required: %w", domain.ErrInvalid)
	}
	requestID, timestamp, err := params.replayIdentity()
	if err != nil {
		return nil, err
	}
	owner, auth, err := m.taskRunAuthority(ctx, ws, execution.ActionAppendLog, id)
	if err != nil {
		return nil, fmt.Errorf("authorize task run log append: %w", err)
	}
	appendLog := execution.AppendLogCommand{
		WorkspaceKey: ws,
		RequestID:    requestID,
		Owner:        owner,
		Stream:       params.Stream,
		Text:         *params.Text,
		Timestamp:    timestamp,
	}
	entry, err := m.execution.AppendLog(ctx, auth, appendLog)
	if err != nil {
		return nil, fmt.Errorf("append task run log: %w", err)
	}
	return logEntryResult(entry), nil
}

// taskRunLogResult is the camelCase wire view of a log append.
type taskRunLogResult struct {
	TaskRunID string    `json:"taskRunId"`
	Sequence  int64     `json:"sequence"`
	Stream    string    `json:"stream"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

func logEntryResult(entry execution.LogEntry) any {
	return taskRunLogResult{
		TaskRunID: entry.TaskRunID,
		Sequence:  entry.Sequence,
		Stream:    entry.Stream,
		Text:      entry.Text,
		Timestamp: entry.Timestamp,
	}
}

// completeParams is the complete op request body (camelCase mirror of the
// fenced fleet-db completion the runner used to post directly).
type completeParams struct {
	CompletionID        string            `json:"completionId"`
	Status              string            `json:"status"`
	ExitCode            *int              `json:"exitCode"`
	LogsRef             string            `json:"logsRef"`
	ArtifactsRef        string            `json:"artifactsRef"`
	RequiredArtifactIDs []string          `json:"requiredArtifactIds"`
	RequireArtifacts    *bool             `json:"requireArtifacts"`
	InputTokens         int64             `json:"inputTokens"`
	OutputTokens        int64             `json:"outputTokens"`
	CacheReadTokens     int64             `json:"cacheReadTokens"`
	CacheWriteTokens    int64             `json:"cacheWriteTokens"`
	EstimatedCostUSD    float64           `json:"estimatedCostUsd"`
	RuntimeMetadata     map[string]string `json:"runtimeMetadata"`
	ErrorClass          string            `json:"errorClass"`
	ErrorMessage        string            `json:"errorMessage"`
	CloseTask           bool              `json:"closeTask"`
	CloseReason         string            `json:"closeReason"`
}

func (p completeParams) finalizeCommand(ws string, owner execution.Owner, now time.Time) execution.FinalizeCommand {
	completionID := strings.TrimSpace(p.CompletionID)
	if completionID == "" {
		completionID = "complete-" + owner.ResourceID
	}
	status := executionStatusFromTaskRunWire(p.Status)
	requireArtifacts := len(p.RequiredArtifactIDs) > 0
	if p.RequireArtifacts != nil {
		requireArtifacts = *p.RequireArtifacts
	}
	return execution.FinalizeCommand{
		WorkspaceKey: ws,
		RequestID:    completionID,
		Owner:        owner,
		Classification: execution.ExitClassification{
			Status:     status,
			ErrorClass: p.ErrorClass,
			Summary:    p.ErrorMessage,
		},
		ExitCode:            p.ExitCode,
		LogsRef:             p.LogsRef,
		ArtifactsRef:        p.ArtifactsRef,
		RequiredArtifactIDs: p.RequiredArtifactIDs,
		RequireArtifacts:    requireArtifacts,
		InputTokens:         p.InputTokens,
		OutputTokens:        p.OutputTokens,
		CacheReadTokens:     p.CacheReadTokens,
		CacheWriteTokens:    p.CacheWriteTokens,
		EstimatedCostUSD:    p.EstimatedCostUSD,
		RuntimeMetadata:     p.RuntimeMetadata,
		CloseWorkItem:       p.CloseTask,
		CloseReason:         p.CloseReason,
		FinishedAt:          now,
	}
}

func executionStatusFromTaskRunWire(raw string) execution.Status {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "completed", "succeeded":
		return execution.StatusSucceeded
	case "failed":
		return execution.StatusFailed
	case "blocked":
		return execution.StatusBlocked
	case "cancelled", "canceled":
		return execution.StatusCancelled
	default:
		return execution.Status(strings.TrimSpace(raw))
	}
}

func (m *Module) complete(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error) {
	params, err := decodeParams[completeParams](body)
	if err != nil {
		return nil, err
	}
	owner, auth, err := m.taskRunAuthority(ctx, ws, execution.ActionFinalize, id)
	if err != nil {
		return nil, fmt.Errorf("authorize task run completion: %w", err)
	}
	complete := params.finalizeCommand(ws, owner, m.now())
	_, err = m.execution.Finalize(ctx, auth, complete)
	if err != nil {
		return nil, fmt.Errorf("complete task run: %w", err)
	}
	run, err := m.store.TaskRuns().Get(ctx, ws, id.TaskRunID)
	if err != nil {
		return nil, fmt.Errorf("load completed task run: %w", err)
	}
	return map[string]any{
		"completion": map[string]any{
			"completionId": complete.RequestID,
			"artifactIds":  complete.RequiredArtifactIDs,
		},
		"taskRun": driverpkg.TaskRunResultFromDomain(run),
	}, nil
}

// opError is the structured v2 error envelope, shape-identical to the
// driver-op API: {code, message, retryable} plus an optional additive
// details object.
type opError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeOpError(w http.ResponseWriter, status int, code, message string, retryable bool) {
	writeOpErrorDetails(w, status, code, message, retryable, nil)
}

func writeOpErrorDetails(w http.ResponseWriter, status int, code, message string, retryable bool, details map[string]any) {
	writeJSON(w, status, map[string]any{"error": opError{Code: code, Message: message, Retryable: retryable, Details: details}})
}

// writeDomainOpError maps domain sentinel errors onto the structured error
// envelope. Lease-verification failures are 401 (the credential is invalid,
// superseded, or the run is no longer live) so runners distinguish "my lease
// is dead" from op-level conflicts.
func writeDomainOpError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errLeaseDenied):
		writeOpError(w, http.StatusUnauthorized, "lease_denied", err.Error(), false)
	case errors.Is(err, domain.ErrNotFound):
		writeOpError(w, http.StatusNotFound, "not_found", err.Error(), false)
	case errors.Is(err, execution.ErrNotFound):
		writeOpError(w, http.StatusNotFound, "not_found", err.Error(), false)
	case errors.Is(err, domain.ErrNotOwner):
		writeOpError(w, http.StatusForbidden, "not_owner", err.Error(), false)
	case errors.Is(err, domain.ErrInvalidTransition):
		writeOpError(w, http.StatusConflict, "invalid_transition", err.Error(), false)
	case errors.Is(err, execution.ErrInvalidTransition):
		writeOpError(w, http.StatusConflict, "invalid_transition", err.Error(), false)
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, execution.ErrConflict):
		writeOpError(w, http.StatusConflict, "conflict", err.Error(), false)
	case errors.Is(err, domain.ErrInvalid):
		writeOpError(w, http.StatusBadRequest, "invalid", err.Error(), false)
	case errors.Is(err, execution.ErrInvalid):
		writeOpError(w, http.StatusBadRequest, "invalid", err.Error(), false)
	case errors.Is(err, execution.ErrFenceConflict), errors.Is(err, authority.ErrAdmissionDenied):
		writeOpError(w, http.StatusForbidden, "not_owner", err.Error(), false)
	case errors.Is(err, execution.ErrUnavailable), errors.Is(err, artifactsmodule.ErrUnavailable):
		writeOpError(w, http.StatusServiceUnavailable, "unavailable", err.Error(), true)
	case errors.Is(err, context.DeadlineExceeded):
		writeOpError(w, http.StatusGatewayTimeout, "timeout", err.Error(), true)
	case errors.Is(err, context.Canceled):
		writeOpError(w, 499, "canceled", err.Error(), true)
	default:
		writeOpError(w, http.StatusInternalServerError, "internal", err.Error(), false)
	}
}
