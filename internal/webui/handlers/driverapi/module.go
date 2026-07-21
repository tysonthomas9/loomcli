// Package driverapi serves the driver-op HTTP API (SDK v2 transport): the
// same operations the hidden `loom driver` CLI subcommands expose, but over
// loom serve so workflow bundles talk HTTP instead of spawning CLI
// subprocesses. New/v2 surface: camelCase JSON on the wire, structured
// errors {code, message, retryable}.
//
// Authentication is run-scoped. The preferred credential is a short-lived
// run-scoped Bearer token (see internal/driver run_token.go): the server
// derives the parent DriverRun identity (run/node/lease/fencing) from the
// token claims, so workflows never handle fencing headers or ambient creds.
// The legacy transport — the X-Loom-Driver-* header quad plus the optional
// shared static API token — keeps working for CLI subcommands and ops
// tooling. Either way the resolved identity is verified through the same
// fenced-heartbeat path the CLI uses, which is also what revokes tokens:
// terminal runs and superseded leases reject regardless of token expiry.
package driverapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/epicrunner"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/roles"
)

// maxDriverOpBodyBytes caps inbound driver-op payloads.
const maxDriverOpBodyBytes = 8 << 20

// Driver identity headers. Mirrors of the LOOM_DRIVER_* env vars the CLI
// transport resolves.
const (
	HeaderDriverRunID        = "X-Loom-Driver-Run-Id"
	HeaderDriverNodeID       = "X-Loom-Driver-Node-Id"
	HeaderDriverLeaseID      = "X-Loom-Driver-Lease-Id"
	HeaderDriverLeaseToken   = "X-Loom-Driver-Lease-Token"   //nolint:gosec // header name, not a credential value
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
	// APIBaseURL is this serve process's own driver/task-run API base URL,
	// handed to bridge-spawned task runners as LOOM_TASK_RUN_API_URL so they
	// talk back to serve with their lease token instead of dialing fleet-db.
	// Task-run preflight fails closed when this is empty.
	APIBaseURL string
	// APIToken, when non-empty, must be presented by clients as
	// "Authorization: Bearer <token>". Requests authenticated by a valid
	// run-scoped token (RunTokenKey) are exempt: workflow calls are
	// token-only.
	APIToken string //nolint:gosec // G117: driver API bearer token intentionally carried by handler config.
	// RunTokenKey is the HS256 signing key for run-scoped driver tokens
	// (internal/driver ParseRunToken). Nil disables the run-token auth path;
	// the legacy header-quad transport is unaffected.
	RunTokenKey []byte
	// WorktreePath is the working directory handed to the host-bridge task
	// executor for exec-task. Defaults to the server's working directory.
	WorktreePath string
	// LocalSettingsDir is the desktop-local settings directory exposed only to
	// bundled local-task-runner executions.
	LocalSettingsDir string
	// IssueBackends overrides the default fleet-db issue backend factory.
	IssueBackends IssueBackendFactory
	// Dispatcher is the connector egress choke point for connector-dispatch.
	// Nil means connector egress is unconfigured and fails closed (see
	// connectors.go).
	Dispatcher *connector.Dispatcher
	// WorkflowEventing is the named application workflow behind emit-event.
	// Nil leaves that mutation inert; there is no legacy InternalSource or
	// direct Store fallback.
	WorkflowEventing *workfloweventing.Workflow
	// EventAwaits preserves the post-admission AW7 notification through a
	// narrow port while await ownership remains in the legacy trigger package.
	EventAwaits          WorkflowEventAwaitDispatcher
	Execution            execution.DriverRunAPI
	ExecutionAuthorities execution.DriverRunAuthorityResolver
	TaskRunRequests      execution.TaskRunRequestAPI
	TaskRunRecovery      execution.TaskRunRecoveryAPI
	TaskRuns             execution.TaskRunAPI
	TaskRunAuthorities   execution.TaskRunAuthorityResolver
	WorkflowCatalog      workflowcatalog.API
	// Artifacts is injected into the host bridge used by exec-task. There is
	// no production Store.Artifacts compatibility fallback.
	Artifacts artifactsmodule.API
}

// Module serves the workspace-scoped driver-op routes.
type Module struct {
	store                store.Store
	apiToken             string
	runTokenKey          []byte
	apiBaseURL           string
	worktreePath         string
	localSettingsDir     string
	issueBackends        IssueBackendFactory
	dispatcher           *connector.Dispatcher
	workflowEventing     *workfloweventing.Workflow
	eventAwaits          WorkflowEventAwaitDispatcher
	execution            execution.DriverRunAPI
	executionAuthorities execution.DriverRunAuthorityResolver
	taskRunRequests      execution.TaskRunRequestAPI
	taskRunRecovery      execution.TaskRunRecoveryAPI
	taskRuns             execution.TaskRunAPI
	taskRunAuthorities   execution.TaskRunAuthorityResolver
	workflowCatalog      workflowcatalog.API
	artifacts            artifactsmodule.API
	ops                  map[string]opHandler

	// Watch stream cadence (see watch.go). Defaults set in NewModule;
	// overridden in tests.
	watchPollInterval      time.Duration
	watchHeartbeatInterval time.Duration
	watchReconcileInterval time.Duration

	// deliverAssignment is a test seam over the driver's lead-assignment
	// delivery facade.
	deliverAssignment func(ctx context.Context, st store.Store, workspace, leadName string) (driverpkg.AgentMessageDeliveryResult, error)
}

// NewModule constructs the driver API module. Returns nil-safe behavior: with
// a nil store, Register registers nothing.
func NewModule(cfg Config) *Module { //nolint:funlen // Operation registration is an explicit capability table.
	m := &Module{
		store:                cfg.Store,
		apiToken:             strings.TrimSpace(cfg.APIToken),
		runTokenKey:          cfg.RunTokenKey,
		apiBaseURL:           strings.TrimSpace(cfg.APIBaseURL),
		worktreePath:         cfg.WorktreePath,
		localSettingsDir:     strings.TrimSpace(cfg.LocalSettingsDir),
		issueBackends:        cfg.IssueBackends,
		dispatcher:           cfg.Dispatcher,
		workflowEventing:     cfg.WorkflowEventing,
		eventAwaits:          cfg.EventAwaits,
		execution:            cfg.Execution,
		executionAuthorities: cfg.ExecutionAuthorities,
		taskRunRequests:      cfg.TaskRunRequests,
		taskRunRecovery:      cfg.TaskRunRecovery,
		taskRuns:             cfg.TaskRuns,
		taskRunAuthorities:   cfg.TaskRunAuthorities,
		workflowCatalog:      cfg.WorkflowCatalog,
		artifacts:            cfg.Artifacts,

		watchPollInterval:      defaultWatchPollInterval,
		watchHeartbeatInterval: defaultWatchHeartbeatInterval,
		watchReconcileInterval: defaultWatchReconcileInterval,

		deliverAssignment: driverpkg.DeliverLeadAssignmentForDriver,
	}
	m.ops = map[string]opHandler{
		"claim-ready":                     m.claimReady,
		"claim-task":                      m.claimTask,
		"binding-config":                  m.bindingConfig,
		"role-get":                        m.roleGet,
		"epic-get":                        m.epicGet,
		"epic-snapshot":                   m.epicSnapshot,
		"list-agents":                     m.listAgents,
		"agent-orchestration-session":     m.agentOrchestrationSession,
		"update-agent-parent":             m.updateAgentParent,
		"deliver-lead-assignment":         m.deliverLeadAssignment,
		"deliver-agent-message":           m.deliverAgentMessage,
		"exec-task":                       m.execTask,
		"task-run-get":                    m.taskRunGet,
		"active-task-runs":                m.activeTaskRuns,
		"recover-stale-tasks":             m.recoverStaleTasks,
		"complete-task":                   m.completeTask,
		"task-diff":                       m.taskDiff,
		"release-task":                    m.releaseTask,
		"connector-dispatch":              m.connectorDispatch,
		"emit-event":                      m.emitEvent,
		"issue-get":                       m.issueGet,
		"issue-list":                      m.issueList,
		"issue-list-comments":             m.issueListComments,
		"issue-comment":                   m.issueComment,
		"issue-update":                    m.issueUpdate,
		"issue-block-repository-required": m.issueBlockRepositoryRequired,
		"issue-add-label":                 m.issueAddLabel,
		"issue-remove-label":              m.issueRemoveLabel,
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
	// verify-run is an internal CLI ownership handshake, not part of the
	// frozen SDK operation table below. Keep it on an explicit route so adding
	// the handshake does not silently expand the public driver-op surface.
	mux.HandleFunc("POST /api/workspaces/{ws}/driver/verify-run", m.handleVerifyRun)
	mux.HandleFunc("POST /api/workspaces/{ws}/driver/{op}", m.handleOp)
	mux.HandleFunc("GET /api/workspaces/{ws}/driver/watch/epic", m.handleWatchEpic)
	// Await-event ops (AW9): two-segment paths the {op} pattern cannot match.
	mux.HandleFunc("POST /api/workspaces/{ws}/driver/events/await", m.handleAwaitEvent)
	mux.HandleFunc("GET /api/workspaces/{ws}/driver/events/awaits", m.handleListAwaits)
	// Composition ops (AW10): same two-segment-path situation.
	mux.HandleFunc("POST /api/workspaces/{ws}/driver/workflows/start", m.handleWorkflowsStart)
	mux.HandleFunc("POST /api/workspaces/{ws}/driver/workflows/await", m.handleWorkflowsAwait)
}

func (m *Module) handleVerifyRun(w http.ResponseWriter, r *http.Request) {
	tokenID, ok := m.authenticate(w, r)
	if !ok {
		return
	}
	m.serveAuthorizedOp(w, r, m.verifyRun, tokenID)
}

// driverIdentity is the per-request parent DriverRun identity resolved from
// the request headers.
type driverIdentity struct {
	RunID      string
	NodeID     string
	LeaseID    string
	LeaseToken string
	fence      string
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
	tokenID, ok := m.authenticate(w, r)
	if !ok {
		return
	}
	op := strings.TrimSpace(r.PathValue("op"))
	handler, ok := m.ops[op]
	if !ok {
		writeOpErrorDetails(w, http.StatusNotFound, "unknown_op", fmt.Sprintf("unknown driver op %q", op), false, map[string]any{"op": op})
		return
	}
	m.serveAuthorizedOp(w, r, handler, tokenID)
}

// serveAuthorizedOp runs the shared post-authenticate op pipeline (body read,
// identity resolution, handler dispatch, error envelope) for both the generic
// {op} route and explicitly registered op paths (events/await). tokenID is
// the run-token identity from authenticate, nil on the legacy header path.
func (m *Module) serveAuthorizedOp(w http.ResponseWriter, r *http.Request, handler opHandler, tokenID *driverIdentity) {
	ws := r.PathValue("ws")
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDriverOpBodyBytes))
	if err != nil {
		writeOpError(w, http.StatusBadRequest, "invalid", "read driver op payload: "+err.Error(), false)
		return
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		body = []byte("{}")
	}
	id, ok := requestIdentity(w, r, tokenID)
	if !ok {
		return
	}
	result, err := handler(r.Context(), ws, id, body)
	if err != nil {
		writeDomainOpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// driverIdentityFromHeaders resolves the per-request parent DriverRun
// identity from the driver headers.
func driverIdentityFromHeaders(r *http.Request) driverIdentity {
	return driverIdentity{
		RunID:      strings.TrimSpace(r.Header.Get(HeaderDriverRunID)),
		NodeID:     strings.TrimSpace(r.Header.Get(HeaderDriverNodeID)),
		LeaseID:    strings.TrimSpace(r.Header.Get(HeaderDriverLeaseID)),
		LeaseToken: strings.TrimSpace(r.Header.Get(HeaderDriverLeaseToken)),
		fence:      r.Header.Get(HeaderDriverFencingToken),
	}
}

type opHandler func(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error)

// verifyParent proves the caller owns a running parent DriverRun.
func (m *Module) verifyParent(ctx context.Context, ws string, id driverIdentity) (*domain.DriverRun, error) {
	if m.execution == nil || m.executionAuthorities == nil {
		return nil, fmt.Errorf("execution DriverRun verification capability is unavailable: %w", execution.ErrUnavailable)
	}
	owner, err := driverRunExecutionOwner(id, id.RunID)
	if err != nil {
		return nil, err
	}
	auth, err := m.executionAuthorities.ResolveDriverRunAuthority(ctx, ws, execution.ActionHeartbeatDriverRun, owner)
	if err != nil {
		return nil, err
	}
	run, err := m.execution.HeartbeatDriverRun(ctx, auth, execution.DriverRunHeartbeatCommand{
		WorkspaceKey: ws, Owner: owner, At: time.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	return driverpkg.LegacyDriverRunSnapshot(run)
}

func driverRunExecutionOwner(id driverIdentity, runID string) (execution.Owner, error) {
	fence, err := id.FencingToken()
	if err != nil {
		return execution.Owner{}, err
	}
	return execution.Owner{
		ResourceKind: execution.ResourceDriverRun, ResourceID: strings.TrimSpace(runID),
		NodeID: id.NodeID, LeaseID: id.LeaseID, LeaseToken: id.LeaseToken, FencingToken: fence,
	}, nil
}

// verifyRun is the run-scoped management handshake used by hidden CLI
// commands before they access issue or agent read models. Authentication and
// owner proof stay server-side and cross the typed Execution heartbeat API;
// the CLI never opens Store to prove a lease.
func (m *Module) verifyRun(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	if err := decodeNoParams(body); err != nil {
		return nil, err
	}
	return m.verifyParent(ctx, ws, id)
}

func decodeNoParams(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var params struct{}
	if err := decoder.Decode(&params); err != nil {
		return fmt.Errorf("decode driver op params: %s: %w", err.Error(), domain.ErrInvalid)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return fmt.Errorf("decode driver op params: %s: %w", err.Error(), domain.ErrInvalid)
	}
	return nil
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
		// Actor is accepted for wire-compat but IGNORED (non-authoritative).
		// SECURITY: the task lock is keyed by the server-derived run actor
		// below, never by caller input — otherwise a run could present a
		// victim's actor label and claim/release under its lease (cross-agent
		// lock takeover in one op call). See releaseTask/claimTask for the
		// symmetric derivation that keeps failure-recovery ownership matched.
		Actor string `json:"actor"`
		// Type optionally narrows the ready queue to one issue type (e.g.
		// "bug"), applied server-side by the ready view.
		Type  string `json:"type"`
		Limit int    `json:"limit"`
	}](body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	epicID := firstNonEmpty(params.EpicID, parent.EpicID, driverpkg.DriverRunPayloadEpicID(parent.Payload))
	actor := driverpkg.DriverRunActor(parent.RunID)
	issueBackend, err := m.issueBackends(ws, actor)
	if err != nil {
		return nil, err
	}
	ready, err := driverpkg.ReadyTaskCandidates(ctx, issueBackend, driverpkg.TaskClaimOptions{
		EpicID: epicID,
		Type:   strings.TrimSpace(params.Type),
		Limit:  params.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list ready tasks: %w", err)
	}
	for _, issue := range ready {
		claimed, claimErr := m.claimDriverRunWorkItem(ctx, ws, id, parent, issue)
		if claimErr == nil {
			return claimed, nil
		}
		if errors.Is(claimErr, execution.ErrConflict) {
			continue
		}
		return nil, fmt.Errorf("claim ready task %q: %w", issue.ID, claimErr)
	}
	return nil, nil
}

// claimTask claims one SPECIFIC ready task by id (GAP B): the event-driven
// counterpart to claim-ready, which pulls in queue order. taskId is the caller's
// legitimate target; every other input follows the claim-ready auth/provenance
// model — the parent run is proven via the run token (verifyParent) and the
// claim actor is ALWAYS derived server-side from that run. SECURITY: a body
// actor is NOT honored for the lock (it is decoded for wire-compat but ignored)
// — the lock is keyed by DriverRunActor(parent.RunID) so a run can only ever
// claim under its own lease, never a victim's actor label. epicId is an OPTIONAL
// narrowing hint the caller may pass; it is NOT defaulted from the parent run so
// a task under any epic can be targeted. Not-ready / already-claimed surfaces as
// a conflict (409).
func (m *Module) claimTask(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		TaskID string `json:"taskId"`
		// Actor: accepted for wire-compat, IGNORED. See the security note above.
		Actor  string `json:"actor"`
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
	if strings.TrimSpace(params.TaskID) == "" {
		return nil, fmt.Errorf("taskId required: %w", domain.ErrInvalid)
	}
	actor := driverpkg.DriverRunActor(parent.RunID)
	issueBackend, err := m.issueBackends(ws, actor)
	if err != nil {
		return nil, err
	}
	issue, err := driverpkg.ReadyTaskByID(ctx, issueBackend, driverpkg.TaskClaimByIDOptions{
		TaskID: params.TaskID,
		EpicID: strings.TrimSpace(params.EpicID),
		Limit:  params.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("claim task: %w", err)
	}
	claimed, err := m.claimDriverRunWorkItem(ctx, ws, id, parent, *issue)
	if err != nil {
		return nil, fmt.Errorf("claim task: %w", err)
	}
	return claimed, nil
}

func (m *Module) claimDriverRunWorkItem(
	ctx context.Context,
	ws string,
	id driverIdentity,
	parent *domain.DriverRun,
	issue backend.IssueData,
) (*driverpkg.ClaimedTask, error) {
	if m.execution == nil || m.executionAuthorities == nil {
		return nil, fmt.Errorf("execution DriverRun Work Item claim capability is unavailable: %w", execution.ErrUnavailable)
	}
	owner, err := driverRunExecutionOwner(id, parent.RunID)
	if err != nil {
		return nil, err
	}
	auth, err := m.executionAuthorities.ResolveDriverRunAuthority(ctx, ws, execution.ActionClaimDriverRunWorkItem, owner)
	if err != nil {
		return nil, fmt.Errorf("resolve DriverRun Work Item claim authority: %w", err)
	}
	requestID := execution.ClaimDriverRunWorkItemRequestID(parent.RunID, issue.ID)
	result, err := m.execution.ClaimDriverRunWorkItem(ctx, auth, execution.ClaimDriverRunWorkItemCommand{
		WorkspaceKey: ws, RequestID: requestID, Owner: owner, WorkItemID: issue.ID, ClaimedAt: time.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	if result.WorkItem == nil || result.Action == nil {
		return nil, fmt.Errorf("typed Work Item claim returned no committed envelope: %w", execution.ErrConflict)
	}
	committed := backend.IssueData{
		ID: result.WorkItem.WorkItemID, Title: result.WorkItem.Title, Status: result.WorkItem.Status,
		Priority: result.WorkItem.Priority, IssueType: result.WorkItem.IssueType, Assignee: result.WorkItem.Assignee,
		Labels: append([]string(nil), result.WorkItem.Labels...), SourceRepo: result.WorkItem.SourceRepo,
		Parent: result.WorkItem.ParentID, UpdatedAt: result.WorkItem.UpdatedAt,
	}
	return driverpkg.ClaimedTaskFromIssue(committed, driverpkg.DriverRunActor(parent.RunID), result.Action.ActionID, result.Action.CreatedAt), nil
}

// roleGet returns a Role (behavior-config) record plus its prompt body (GAP C):
// the read-only surface a prompt agent uses to materialize its role's prompt at
// dispatch time, so "one prompt edit updates every agent" without passing the
// prompt as raw input. Workspace-scoped, run-token authenticated like the other
// driverapi reads (verifyParent). The prompt body is loaded through the roles
// module's shared loader (roles.ReadPromptBody) — the one place role prompts are
// read from <workspace>/.loom/prompts.
func (m *Module) roleGet(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		Name string `json:"name"`
	}](body)
	if err != nil {
		return nil, err
	}
	if _, err := m.verifyParent(ctx, ws, id); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return nil, fmt.Errorf("name required: %w", domain.ErrInvalid)
	}
	role, err := m.store.Roles().Get(ctx, ws, name)
	if err != nil {
		return nil, fmt.Errorf("get role: %w", err)
	}
	return map[string]any{"role": role, "prompt": roles.ReadPromptBody(role)}, nil
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
	if delivery.State != driverpkg.AgentMessageDeliveryStateDelivered && delivery.State != driverpkg.AgentMessageDeliveryStateUnsupported {
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
	return delivery, nil
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

// --- issue (card) ops (P0-1): thin IssueBackend pass-throughs so a workflow can
// read and mutate a fleet-db card. Auth is the run token via verifyParent; the
// actor is derived from the parent run (as in claimReady/releaseTask). The
// loom.issue.* SDK surface is generated from sdk/op-spec.mjs.

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// opError is the structured v2 error envelope. The shape is FROZEN as the
// SDK v1 contract (sdk/api-surface.v1.json): {code, message, retryable}
// with an OPTIONAL additive details object for machine-readable context.
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

// writeOpErrorDetails writes the envelope with the optional details object;
// details is additive — clients that predate it ignore the extra key.
func writeOpErrorDetails(w http.ResponseWriter, status int, code, message string, retryable bool, details map[string]any) {
	writeJSON(w, status, map[string]any{"error": opError{Code: code, Message: message, Retryable: retryable, Details: details}})
}

func writeCodedOpError(w http.ResponseWriter, err error) bool {
	var coded *codedOpError
	if !errors.As(err, &coded) {
		return false
	}
	writeOpErrorDetails(w, coded.status, coded.code, coded.Error(), coded.retryable, coded.details)
	return true
}

func writeSpecializedOpError(w http.ResponseWriter, err error) bool {
	return writeConnectorOpError(w, err) || writeAwaitOpError(w, err) || writeCodedOpError(w, err)
}

// writeDomainOpError maps domain sentinel errors onto the structured error
// envelope. Defaults to a non-retryable internal error: only transient
// classes (timeouts, cancellation) advertise retryability.
func writeDomainOpError(w http.ResponseWriter, err error) {
	if writeSpecializedOpError(w, err) || writeBackendDomainOpError(w, err) ||
		writeAutomationDomainOpError(w, err) || writeExecutionDomainOpError(w, err) {
		return
	}
	writeBaseDomainOpError(w, err)
}

func writeBackendDomainOpError(w http.ResponseWriter, err error) bool {
	switch {
	case backend.IsKind(err, backend.KindValidation):
		writeOpError(w, http.StatusBadRequest, "invalid", err.Error(), false)
	case backend.IsKind(err, backend.KindNotFound):
		writeOpError(w, http.StatusNotFound, "not_found", err.Error(), false)
	case backend.IsKind(err, backend.KindConflict):
		writeOpError(w, http.StatusConflict, "conflict", err.Error(), false)
	case backend.IsKind(err, backend.KindNotImplemented):
		writeOpError(w, http.StatusNotImplemented, "not_implemented", err.Error(), false)
	case backend.IsKind(err, backend.KindUnavailable):
		writeOpError(w, http.StatusServiceUnavailable, "unavailable", err.Error(), true)
	case backend.IsKind(err, backend.KindTimeout):
		writeOpError(w, http.StatusGatewayTimeout, "timeout", err.Error(), true)
	case backend.IsKind(err, backend.KindCanceled):
		writeOpError(w, 499, "canceled", err.Error(), true)
	case backend.IsKind(err, backend.KindInternal):
		writeOpError(w, http.StatusInternalServerError, "internal", err.Error(), false)
	default:
		return false
	}
	return true
}

func writeAutomationDomainOpError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, workfloweventing.ErrInvalidRequest), errors.Is(err, automation.ErrInvalid), errors.Is(err, automation.ErrWrongWorkspace):
		writeOpError(w, http.StatusBadRequest, "invalid", err.Error(), false)
	case errors.Is(err, automation.ErrNotFound), errors.Is(err, automation.ErrNoMatchingBinding), errors.Is(err, automation.ErrParentEventNotFound):
		writeOpError(w, http.StatusNotFound, "not_found", err.Error(), false)
	case errors.Is(err, automation.ErrConflict):
		writeOpError(w, http.StatusConflict, "conflict", err.Error(), false)
	case errors.Is(err, workfloweventing.ErrUnavailable), errors.Is(err, automation.ErrUnavailable):
		writeOpError(w, http.StatusServiceUnavailable, "unavailable", err.Error(), true)
	default:
		return false
	}
	return true
}

func writeExecutionDomainOpError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, execution.ErrNotFound):
		writeOpError(w, http.StatusNotFound, "not_found", err.Error(), false)
	case errors.Is(err, execution.ErrFenceConflict), errors.Is(err, authority.ErrInvalidScope):
		writeOpError(w, http.StatusForbidden, "not_owner", err.Error(), false)
	case errors.Is(err, execution.ErrUnschedulable):
		writeOpError(w, http.StatusConflict, "unschedulable", err.Error(), true)
	case errors.Is(err, execution.ErrInvalidTransition):
		writeOpError(w, http.StatusConflict, "invalid_transition", err.Error(), false)
	case errors.Is(err, execution.ErrConflict):
		writeOpError(w, http.StatusConflict, "conflict", err.Error(), false)
	case errors.Is(err, execution.ErrInvalid):
		writeOpError(w, http.StatusBadRequest, "invalid", err.Error(), false)
	case errors.Is(err, execution.ErrUnavailable):
		writeOpError(w, http.StatusServiceUnavailable, "unavailable", err.Error(), true)
	default:
		return false
	}
	return true
}

func writeBaseDomainOpError(w http.ResponseWriter, err error) {
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
