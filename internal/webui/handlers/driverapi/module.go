// Package driverapi serves the driver-op HTTP API (SDK v2 transport): the
// same operations the hidden `loom driver` CLI subcommands expose, but over
// loom serve so workflow bundles talk HTTP instead of spawning CLI
// subprocesses. New/v2 surface: camelCase JSON on the wire, structured
// errors {code, message, retryable}.
//
// Authentication is run-scoped: every request carries the parent DriverRun
// identity (X-Loom-Driver-Run-Id plus owner credentials), verified through
// the same fenced-heartbeat path the CLI uses. When the server is configured
// with a shared driver API token, requests must additionally present it as a
// bearer token.
package driverapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/epicrunner"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

// maxDriverOpBodyBytes caps inbound driver-op payloads.
const maxDriverOpBodyBytes = 8 << 20

// Driver identity headers. Mirrors of the LOOM_DRIVER_* env vars the CLI
// transport resolves.
const (
	HeaderDriverRunID        = "X-Loom-Driver-Run-Id"
	HeaderDriverNodeID       = "X-Loom-Driver-Node-Id"
	HeaderDriverLeaseID      = "X-Loom-Driver-Lease-Id"
	HeaderDriverFencingToken = "X-Loom-Driver-Fencing-Token" //nolint:gosec // header name, not a credential
)

// IssueBackendFactory builds a workspace-scoped fleet-db issue backend acting
// as the given actor. Overridable in tests.
type IssueBackendFactory func(ws, actor string) (backend.IssueBackend, error)

// Config wires the module's dependencies.
type Config struct {
	Store store.Store
	// FleetBaseURL is the fleet-db HTTP base URL used to build issue
	// backends for task claim/release and epic reads.
	FleetBaseURL string
	// APIToken, when non-empty, must be presented by clients as
	// "Authorization: Bearer <token>".
	APIToken string
	// WorktreePath is the working directory handed to the host-bridge task
	// executor for exec-task. Defaults to the server's working directory.
	WorktreePath string
	// IssueBackends overrides the default fleet-db issue backend factory.
	IssueBackends IssueBackendFactory
	// Dispatcher is the connector egress choke point for connector-dispatch.
	// Nil means connector egress is unconfigured and fails closed (see
	// connectors.go).
	Dispatcher *connector.Dispatcher
}

// Module serves the workspace-scoped driver-op routes.
type Module struct {
	store         store.Store
	apiToken      string
	worktreePath  string
	issueBackends IssueBackendFactory
	dispatcher    *connector.Dispatcher
	ops           map[string]opHandler

	// internalEvents is the C14 internal-event loopback ingress backing the
	// emit-event op (see internal/trigger/internal_source.go).
	internalEvents *trigger.InternalSource

	// Watch stream cadence (see watch.go). Defaults set in NewModule;
	// overridden in tests.
	watchPollInterval      time.Duration
	watchHeartbeatInterval time.Duration
	watchReconcileInterval time.Duration

	// deliverAssignment is a test seam over
	// leadcontrol.DeliverCurrentAssignment for deliver-lead-assignment.
	deliverAssignment func(ctx context.Context, st store.Store, workspace, leadName string) (*leadcontrol.DeliveryResult, error)
}

// NewModule constructs the driver API module. Returns nil-safe behavior: with
// a nil store, Register registers nothing.
func NewModule(cfg Config) *Module {
	m := &Module{
		store:         cfg.Store,
		apiToken:      strings.TrimSpace(cfg.APIToken),
		worktreePath:  cfg.WorktreePath,
		issueBackends: cfg.IssueBackends,
		dispatcher:    cfg.Dispatcher,

		watchPollInterval:      defaultWatchPollInterval,
		watchHeartbeatInterval: defaultWatchHeartbeatInterval,
		watchReconcileInterval: defaultWatchReconcileInterval,

		deliverAssignment: leadcontrol.DeliverCurrentAssignment,

		internalEvents: &trigger.InternalSource{Store: cfg.Store},
	}
	m.ops = map[string]opHandler{
		"claim-ready":                 m.claimReady,
		"epic-get":                    m.epicGet,
		"epic-snapshot":               m.epicSnapshot,
		"list-agents":                 m.listAgents,
		"agent-orchestration-session": m.agentOrchestrationSession,
		"update-agent-parent":         m.updateAgentParent,
		"deliver-lead-assignment":     m.deliverLeadAssignment,
		"deliver-agent-message":       m.deliverAgentMessage,
		"exec-task":                   m.execTask,
		"task-run-get":                m.taskRunGet,
		"active-task-runs":            m.activeTaskRuns,
		"recover-stale-tasks":         m.recoverStaleTasks,
		"complete-task":               m.completeTask,
		"release-task":                m.releaseTask,
		"connector-dispatch":          m.connectorDispatch,
		"emit-event":                  m.emitEvent,
	}
	if m.worktreePath == "" {
		if wd, err := os.Getwd(); err == nil {
			m.worktreePath = wd
		}
	}
	if m.issueBackends == nil {
		m.issueBackends = defaultIssueBackends(cfg.FleetBaseURL)
	}
	return m
}

// defaultIssueBackends builds the production issue-backend factory: a
// fleet-db client per (workspace, actor) against the configured base URL.
func defaultIssueBackends(baseURL string) func(ws, actor string) (backend.IssueBackend, error) {
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
	if m.store == nil {
		return
	}
	mux.HandleFunc("POST /api/workspaces/{ws}/driver/{op}", m.handleOp)
	mux.HandleFunc("GET /api/workspaces/{ws}/driver/watch/epic", m.handleWatchEpic)
}

// driverIdentity is the per-request parent DriverRun identity resolved from
// the request headers.
type driverIdentity struct {
	RunID   string
	NodeID  string
	LeaseID string
	fence   string
}

func (id driverIdentity) FencingToken() (int64, error) {
	raw := strings.TrimSpace(id.fence)
	if raw == "" {
		return 0, nil
	}
	token, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || token <= 0 {
		if err == nil {
			err = domain.ErrInvalid
		}
		return 0, fmt.Errorf("parse %s: %w", HeaderDriverFencingToken, err)
	}
	return token, nil
}

func (m *Module) handleOp(w http.ResponseWriter, r *http.Request) {
	if !m.authorize(w, r) {
		return
	}
	ws := r.PathValue("ws")
	op := strings.TrimSpace(r.PathValue("op"))
	handler, ok := m.ops[op]
	if !ok {
		writeOpError(w, http.StatusNotFound, "unknown_op", fmt.Sprintf("unknown driver op %q", op), false)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDriverOpBodyBytes))
	if err != nil {
		writeOpError(w, http.StatusBadRequest, "invalid", "read driver op payload: "+err.Error(), false)
		return
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		body = []byte("{}")
	}
	id := driverIdentity{
		RunID:   strings.TrimSpace(r.Header.Get(HeaderDriverRunID)),
		NodeID:  strings.TrimSpace(r.Header.Get(HeaderDriverNodeID)),
		LeaseID: strings.TrimSpace(r.Header.Get(HeaderDriverLeaseID)),
		fence:   r.Header.Get(HeaderDriverFencingToken),
	}
	if id.RunID == "" {
		writeOpError(w, http.StatusUnauthorized, "unauthenticated", HeaderDriverRunID+" header required", false)
		return
	}
	result, err := handler(r.Context(), ws, id, body)
	if err != nil {
		writeDomainOpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// authorize enforces the optional shared bearer token.
func (m *Module) authorize(w http.ResponseWriter, r *http.Request) bool {
	if m.apiToken == "" {
		return true
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	token, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(m.apiToken)) != 1 {
		writeOpError(w, http.StatusUnauthorized, "unauthenticated", "missing or invalid driver API token", false)
		return false
	}
	return true
}

type opHandler func(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error)

// verifyParent proves the caller owns a running parent DriverRun.
func (m *Module) verifyParent(ctx context.Context, ws string, id driverIdentity) (*domain.DriverRun, error) {
	return driverpkg.VerifyRunningDriverRun(ctx, m.store, ws, id.RunID, id.NodeID, id.LeaseID, id.FencingToken)
}

func decodeParams[T any](body []byte) (T, error) {
	var params T
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&params); err != nil {
		return params, fmt.Errorf("decode driver op params: %s: %w", err.Error(), domain.ErrInvalid)
	}
	return params, nil
}

func (m *Module) claimReady(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		EpicID string `json:"epicId"`
		Actor  string `json:"actor"`
		Limit  int    `json:"limit"`
	}](body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	epicID := firstNonEmpty(params.EpicID, parent.EpicID, driverpkg.DriverRunPayloadEpicID(parent.Payload))
	actor := firstNonEmpty(params.Actor, driverpkg.DriverRunActor(parent.RunID))
	issueBackend, err := m.issueBackends(ws, actor)
	if err != nil {
		return nil, err
	}
	// ClaimReadyTask defaults a non-positive limit itself.
	claimed, err := driverpkg.ClaimReadyTask(ctx, issueBackend, driverpkg.TaskClaimOptions{
		EpicID: epicID,
		Actor:  actor,
		Limit:  params.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("claim ready task: %w", err)
	}
	return claimed, nil
}

func (m *Module) epicGet(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		EpicID string `json:"epicId"`
	}](body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	epicID := firstNonEmpty(params.EpicID, parent.EpicID, driverpkg.DriverRunPayloadEpicID(parent.Payload))
	if epicID == "" {
		return nil, fmt.Errorf("epic id required: %w", domain.ErrInvalid)
	}
	issueBackend, err := m.issueBackends(ws, driverpkg.DriverRunActor(parent.RunID))
	if err != nil {
		return nil, err
	}
	epic, err := issueBackend.Get(ctx, epicID)
	if err != nil {
		return nil, fmt.Errorf("get epic: %w", err)
	}
	return epic, nil
}

func (m *Module) epicSnapshot(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		EpicID string `json:"epicId"`
	}](body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	epicID := firstNonEmpty(params.EpicID, parent.EpicID, driverpkg.DriverRunPayloadEpicID(parent.Payload))
	issueBackend, err := m.issueBackends(ws, driverpkg.DriverRunActor(parent.RunID))
	if err != nil {
		return nil, err
	}
	snapshot, err := driverpkg.LoadEpicSnapshot(ctx, issueBackend, driverpkg.EpicSnapshotOptions{EpicID: epicID})
	if err != nil {
		return nil, fmt.Errorf("snapshot epic: %w", err)
	}
	return snapshot, nil
}

func (m *Module) listAgents(ctx context.Context, ws string, id driverIdentity, _ []byte) (any, error) {
	if _, err := m.verifyParent(ctx, ws, id); err != nil {
		return nil, err
	}
	agents, err := m.store.Agents().List(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	return agents, nil
}

func (m *Module) agentOrchestrationSession(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		Agent string `json:"agent"`
	}](body)
	if err != nil {
		return nil, err
	}
	if _, err := m.verifyParent(ctx, ws, id); err != nil {
		return nil, err
	}
	agentName := strings.TrimSpace(params.Agent)
	if agentName == "" {
		return nil, fmt.Errorf("agent required: %w", domain.ErrInvalid)
	}
	sessionID, err := store.OrchestrationSessionIDFor(ctx, m.store, ws, agentName)
	if err != nil {
		return nil, fmt.Errorf("resolve orchestration session: %w", err)
	}
	return map[string]string{"agentName": agentName, "orchestratorSessionId": sessionID}, nil
}

func (m *Module) updateAgentParent(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		Agent        string `json:"agent"`
		Parent       string `json:"parent"`
		ExpectParent string `json:"expectParent"`
	}](body)
	if err != nil {
		return nil, err
	}
	if _, err := m.verifyParent(ctx, ws, id); err != nil {
		return nil, err
	}
	agentName := strings.TrimSpace(params.Agent)
	parentID := strings.TrimSpace(params.Parent)
	if agentName == "" || parentID == "" {
		return nil, fmt.Errorf("agent and parent required: %w", domain.ErrInvalid)
	}
	unlock, err := epicrunner.AcquireBindLock(ws, agentName)
	if err != nil {
		return nil, fmt.Errorf("acquire agent parent lock: %w", err)
	}
	defer unlock()
	current, err := m.store.Agents().Get(ctx, ws, agentName)
	if err != nil {
		return nil, fmt.Errorf("get agent: %w", err)
	}
	if current.Parent != strings.TrimSpace(params.ExpectParent) {
		return nil, fmt.Errorf("agent %q parent changed from %q to %q: %w", agentName, strings.TrimSpace(params.ExpectParent), current.Parent, domain.ErrConflict)
	}
	updated, err := m.store.Agents().Update(ctx, ws, agentName, store.AgentUpdate{Parent: &parentID})
	if err != nil {
		return nil, fmt.Errorf("update agent parent: %w", err)
	}
	return updated, nil
}

func (m *Module) deliverLeadAssignment(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		Agent string `json:"agent"`
	}](body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	leadName := strings.TrimSpace(params.Agent)
	if leadName == "" {
		return nil, fmt.Errorf("agent required: %w", domain.ErrInvalid)
	}
	// Attempt-then-enqueue: one inline delivery attempt covers the fast
	// path; anything short of delivered/unsupported durably enqueues an
	// outbox row (deduped per driver run + lead) so the server-side
	// dispatcher owns the retries and the workflow calls this op exactly
	// once. The response shape (AgentMessageDeliveryResult) is unchanged.
	delivery, err := m.deliverAssignment(ctx, m.store, ws, leadName)
	if err != nil {
		return nil, fmt.Errorf("deliver lead assignment: %w", err)
	}
	state := leadcontrol.DeliveryStateNone
	if delivery != nil {
		state = delivery.State
	}
	if state != leadcontrol.DeliveryStateDelivered && state != leadcontrol.DeliveryStateUnsupported {
		if _, err := m.store.Outbox().Create(ctx, store.OutboxCreate{
			WorkspaceKey: ws,
			Kind:         domain.OutboxKindLeadAssignment,
			EpicID:       firstNonEmpty(parent.EpicID, driverpkg.DriverRunPayloadEpicID(parent.Payload)),
			DriverRunID:  parent.RunID,
			TargetAgent:  leadName,
			DedupeKey:    "lead-assignment:" + parent.RunID + ":" + leadName,
		}); err != nil {
			return nil, fmt.Errorf("enqueue lead assignment outbox: %w", err)
		}
	}
	return driverpkg.NewAgentMessageDeliveryResult(leadName, delivery), nil
}

func (m *Module) deliverAgentMessage(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		Agent   string `json:"agent"`
		Message string `json:"message"`
	}](body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	agentName := strings.TrimSpace(params.Agent)
	message := strings.TrimSpace(params.Message)
	if agentName == "" || message == "" {
		return nil, fmt.Errorf("agent and message required: %w", domain.ErrInvalid)
	}
	result, err := driverpkg.DeliverAgentMessageForDriver(ctx, m.store, ws, parent.RunID, agentName, message)
	if err != nil {
		return nil, fmt.Errorf("deliver agent message: %w", err)
	}
	return result, nil
}

// execTaskParams is the exec-task request body.
type execTaskParams struct {
	TaskID             string   `json:"taskId"`
	TaskRunID          string   `json:"taskRunId"`
	DriverStepID       string   `json:"driverStepId"`
	WorkerProfileID    string   `json:"workerProfileId"`
	ProviderProfile    string   `json:"providerProfile"`
	ParentSessionID    string   `json:"parentSessionId"`
	RunnerID           string   `json:"runnerId"`
	LeaseToken         string   `json:"leaseToken"`
	SupportedProviders []string `json:"supportedProviders"`
	Capabilities       []string `json:"capabilities"`
	SandboxPlacement   struct {
		Provider  string `json:"provider"`
		SandboxID string `json:"sandboxId"`
		CWD       string `json:"cwd"`
		RepoRef   string `json:"repoRef"`
	} `json:"sandboxPlacement"`
	DeferCompletion bool `json:"deferCompletion"`
	EnqueueOnly     bool `json:"enqueueOnly"`
}

func (p execTaskParams) requestOptions(ws string, id driverIdentity, fencingToken int64) driverpkg.TaskRunRequestOptions {
	return driverpkg.TaskRunRequestOptions{
		WorkspaceKey:       ws,
		DriverRunID:        id.RunID,
		DriverStepID:       p.DriverStepID,
		TaskRunID:          p.TaskRunID,
		TaskID:             p.TaskID,
		WorkerProfileID:    p.WorkerProfileID,
		ProviderProfile:    p.ProviderProfile,
		ParentSessionID:    p.ParentSessionID,
		ParentNodeID:       id.NodeID,
		ParentLeaseID:      id.LeaseID,
		ParentFence:        fencingToken,
		NodeID:             id.NodeID,
		RunnerID:           p.RunnerID,
		LeaseToken:         p.LeaseToken,
		SupportedProviders: p.SupportedProviders,
		Capabilities:       p.Capabilities,
		SandboxPlacement: domain.TaskRunPlacement{
			Provider:  p.SandboxPlacement.Provider,
			SandboxID: p.SandboxPlacement.SandboxID,
			CWD:       p.SandboxPlacement.CWD,
			RepoRef:   p.SandboxPlacement.RepoRef,
		},
		DeferCompletion: p.DeferCompletion,
	}
}

func (m *Module) execTask(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[execTaskParams](body)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.TaskID) == "" {
		return nil, fmt.Errorf("taskId required: %w", domain.ErrInvalid)
	}
	fencingToken, err := id.FencingToken()
	if err != nil {
		return nil, err
	}
	opts := params.requestOptions(ws, id, fencingToken)
	executor := driverpkg.HostBridgeTaskExecutor{
		Store:        m.store,
		WorktreePath: m.worktreePath,
	}
	if params.EnqueueOnly {
		outcome, err := driverpkg.EnqueueTaskRunWithResult(ctx, m.store, opts, executor)
		if err != nil {
			return nil, fmt.Errorf("enqueue task: %w", err)
		}
		return driverpkg.TaskRunResultFromOutcome(outcome), nil
	}
	outcome, err := driverpkg.RequestTaskRunWithResult(ctx, m.store, opts, executor)
	if err != nil {
		return nil, fmt.Errorf("exec task: %w", err)
	}
	return driverpkg.TaskRunResultFromOutcome(outcome), nil
}

func (m *Module) taskRunGet(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		TaskRunID string `json:"taskRunId"`
	}](body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	taskRunID := strings.TrimSpace(params.TaskRunID)
	if taskRunID == "" {
		return nil, fmt.Errorf("taskRunId required: %w", domain.ErrInvalid)
	}
	run, err := m.store.TaskRuns().Get(ctx, ws, taskRunID)
	if err != nil {
		return nil, fmt.Errorf("get task run: %w", err)
	}
	if run.DriverRunID != parent.RunID {
		return nil, fmt.Errorf("task run %q does not belong to driver run %q: %w", taskRunID, parent.RunID, domain.ErrNotFound)
	}
	return driverpkg.TaskRunResultFromDomain(run), nil
}

func (m *Module) activeTaskRuns(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		EpicID string `json:"epicId"`
		Limit  int    `json:"limit"`
	}](body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	epicID := firstNonEmpty(params.EpicID, parent.EpicID, driverpkg.DriverRunPayloadEpicID(parent.Payload))
	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	active, err := driverpkg.ListActiveTaskRuns(ctx, m.store, driverpkg.ActiveTaskRunsOptions{
		WorkspaceKey: ws,
		DriverRunID:  parent.RunID,
		EpicID:       epicID,
		Limit:        limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list active task runs: %w", err)
	}
	return active, nil
}

func (m *Module) recoverStaleTasks(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		StaleBefore   string `json:"staleBefore"`
		MaxAgeSeconds int64  `json:"maxAgeSeconds"`
		ErrorClass    string `json:"errorClass"`
		ErrorMessage  string `json:"errorMessage"`
	}](body)
	if err != nil {
		return nil, err
	}
	// Unlike the CLI path (already inside an authenticated process), the HTTP
	// surface must prove run ownership before failing its task runs.
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	staleBefore := time.Time{}
	if raw := strings.TrimSpace(params.StaleBefore); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, fmt.Errorf("parse staleBefore as RFC3339: %s: %w", err.Error(), domain.ErrInvalid)
		}
		staleBefore = parsed.UTC()
	}
	maxAgeSeconds := params.MaxAgeSeconds
	if maxAgeSeconds <= 0 {
		maxAgeSeconds = 300
	}
	result, err := m.store.DriverRuns().RecoverStaleTaskRuns(ctx, ws, parent.RunID, store.StaleTaskRunRecovery{
		StaleBefore:   staleBefore,
		MaxAgeSeconds: maxAgeSeconds,
		ErrorClass:    firstNonEmpty(params.ErrorClass, "stale_task_run"),
		ErrorMessage:  firstNonEmpty(params.ErrorMessage, "task run heartbeat is stale"),
	})
	if err != nil {
		return nil, fmt.Errorf("recover stale task runs: %w", err)
	}
	return result, nil
}

func (m *Module) completeTask(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		TaskID       string   `json:"taskId"`
		TaskRunID    string   `json:"taskRunId"`
		CompletionID string   `json:"completionId"`
		LeaseToken   string   `json:"leaseToken"`
		ArtifactIDs  []string `json:"artifactIds"`
		LogsRef      string   `json:"logsRef"`
		ArtifactsRef string   `json:"artifactsRef"`
		Reason       string   `json:"reason"`
	}](body)
	if err != nil {
		return nil, err
	}
	if _, err := m.verifyParent(ctx, ws, id); err != nil {
		return nil, err
	}
	taskRunID := strings.TrimSpace(params.TaskRunID)
	if taskRunID == "" {
		return nil, fmt.Errorf("taskRunId is required for fenced driver completion: %w", domain.ErrInvalid)
	}
	result, err := driverpkg.CompleteDriverTaskRun(ctx, m.store.TaskRuns(), ws, taskRunID, driverpkg.DriverTaskRunCompletionOptions{
		TaskID:       params.TaskID,
		CompletionID: params.CompletionID,
		LeaseToken:   params.LeaseToken,
		ArtifactIDs:  params.ArtifactIDs,
		LogsRef:      params.LogsRef,
		ArtifactsRef: params.ArtifactsRef,
		Reason:       params.Reason,
	})
	if err != nil {
		return nil, fmt.Errorf("complete task run: %w", err)
	}
	return result, nil
}

func (m *Module) releaseTask(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		TaskID string `json:"taskId"`
		Actor  string `json:"actor"`
	}](body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.TaskID) == "" {
		return nil, fmt.Errorf("taskId required: %w", domain.ErrInvalid)
	}
	actor := firstNonEmpty(params.Actor, driverpkg.DriverRunActor(parent.RunID))
	issueBackend, err := m.issueBackends(ws, actor)
	if err != nil {
		return nil, err
	}
	result, err := driverpkg.ReleaseTask(ctx, issueBackend, driverpkg.TaskReleaseOptions{
		TaskID: params.TaskID,
		Actor:  actor,
	})
	if err != nil {
		return nil, fmt.Errorf("release task: %w", err)
	}
	return result, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// opError is the structured v2 error envelope.
type opError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeOpError(w http.ResponseWriter, status int, code, message string, retryable bool) {
	writeJSON(w, status, map[string]any{"error": opError{Code: code, Message: message, Retryable: retryable}})
}

// writeDomainOpError maps domain sentinel errors onto the structured error
// envelope. Defaults to a non-retryable internal error: only transient
// classes (timeouts, cancellation) advertise retryability.
func writeDomainOpError(w http.ResponseWriter, err error) {
	if writeConnectorOpError(w, err) {
		return
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeOpError(w, http.StatusNotFound, "not_found", err.Error(), false)
	case errors.Is(err, domain.ErrNotOwner):
		writeOpError(w, http.StatusForbidden, "not_owner", err.Error(), false)
	case errors.Is(err, domain.ErrUnschedulable):
		writeOpError(w, http.StatusConflict, "unschedulable", err.Error(), true)
	case errors.Is(err, domain.ErrInvalidTransition):
		writeOpError(w, http.StatusConflict, "invalid_transition", err.Error(), false)
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrAlreadyClaimed):
		writeOpError(w, http.StatusConflict, "conflict", err.Error(), false)
	case errors.Is(err, domain.ErrInvalid):
		writeOpError(w, http.StatusBadRequest, "invalid", err.Error(), false)
	case errors.Is(err, context.DeadlineExceeded):
		writeOpError(w, http.StatusGatewayTimeout, "timeout", err.Error(), true)
	case errors.Is(err, context.Canceled):
		writeOpError(w, 499, "canceled", err.Error(), true)
	default:
		writeOpError(w, http.StatusInternalServerError, "internal", err.Error(), false)
	}
}
