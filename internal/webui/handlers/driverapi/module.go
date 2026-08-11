// Package driverapi serves the driver-op HTTP API (SDK v2 transport): the
// same operations the hidden `loom driver` CLI subcommands expose, but over
// loom serve so workflow bundles talk HTTP instead of spawning CLI
// subprocesses. New/v2 surface: camelCase JSON on the wire, structured
// errors {code, message, retryable}.
//
// Authentication is run-scoped. The only credential is a short-lived
// run-scoped Bearer token (see internal/driver run_token.go): the server
// derives the parent DriverRun identity (run/node/lease/fencing) from the
// token claims, so workflows never handle fencing headers or ambient creds.
// The resolved identity is verified through the same fenced-heartbeat path
// the CLI uses, which is also what revokes tokens:
// terminal runs and superseded leases reject regardless of token expiry.
package driverapi

import (
	"context"
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
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/store"
	serverhandler "github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// maxDriverOpBodyBytes caps inbound driver-op payloads.
const maxDriverOpBodyBytes = 8 << 20

type IssueBackendFactory func(workspace, actor string) (backend.IssueBackend, error)

// Store is the legacy read-only projection boundary still needed by the
// driver HTTP adapter while mutations enter through capability APIs.
type Store interface {
	store.OrchestrationSessionStore
	Awaits() store.AwaitStore
	TriggerEvents() store.TriggerEventStore
	TriggerBindings() store.TriggerBindingStore
	TriggerDeliveries() store.TriggerDeliveryStore
	Roles() store.RoleStore
	Repos() store.RepoStore
	AgentServices() store.AgentServiceStore
	TaskRuns() store.TaskRunStore
	TaskRunEvents() store.TaskRunEventStore
	DriverRuns() store.DriverRunStore
	Drivers() store.DriverStore
	DriverVersions() store.DriverVersionStore
	Nodes() store.NodeStore
	WorkerProfiles() store.WorkerProfileStore
}

// Module serves the workspace-scoped driver-op routes.
type Module struct {
	store                Store
	runTokenKey          []byte
	apiBaseURL           string
	worktreePath         string
	localSettingsDir     string
	sourceControl        SourceControl
	stackBindings        sourcecontrol.StackBindingResolver
	taskOutcomes         sourcecontrol.TaskOutcomeRecorder
	localRepoPath        func(workspaceKey, repoName string) string
	issueBackends        IssueBackendFactory
	rolePrompts          RolePromptReader
	dispatcher           connectorsmodule.Dispatcher
	workflowEventing     *workfloweventing.Workflow
	eventAwaits          WorkflowEventAwaitDispatcher
	execution            execution.DriverRunAPI
	executionAuthorities execution.DriverRunAuthorityResolver
	agentIdentities      agents.IdentityQueries
	taskRunRequests      execution.TaskRunRequestAPI
	taskRunRecovery      execution.TaskRunRecoveryAPI
	taskRuns             execution.TaskRunAPI
	taskRunAuthorities   execution.TaskRunAuthorityResolver
	workflowCatalog      workflowcatalog.API
	artifacts            Artifacts
	interactionChat      interaction.ChatMessenger
	ops                  map[string]opHandler

	// Watch stream cadence (see watch.go). Defaults set in NewModule;
	// overridden in tests.
	watchPollInterval      time.Duration
	watchHeartbeatInterval time.Duration
	watchReconcileInterval time.Duration

	// deliverAssignment is a test seam over the driver's lead-assignment
	// delivery facade.
	deliverAssignment func(ctx context.Context, st Store, workspace, leadName string) (driverpkg.AgentMessageDeliveryResult, error)
}

// NewModule constructs the driver API module. Returns nil-safe behavior: with
// a nil store, Register registers nothing.
func NewModule(cfg Config) *Module { //nolint:funlen // Operation registration is an explicit capability table.
	if !connectorsmodule.DispatcherAvailable(cfg.Dispatcher) {
		cfg.Dispatcher = nil
	}
	m := &Module{
		store:                cfg.Store,
		runTokenKey:          cfg.RunTokenKey,
		apiBaseURL:           strings.TrimSpace(cfg.APIBaseURL),
		worktreePath:         cfg.WorktreePath,
		localSettingsDir:     strings.TrimSpace(cfg.LocalSettingsDir),
		sourceControl:        cfg.SourceControl,
		stackBindings:        cfg.StackBindings,
		taskOutcomes:         cfg.TaskOutcomes,
		localRepoPath:        cfg.LocalRepoPath,
		issueBackends:        cfg.IssueBackends,
		rolePrompts:          cfg.RolePrompts,
		dispatcher:           cfg.Dispatcher,
		workflowEventing:     cfg.WorkflowEventing,
		eventAwaits:          cfg.EventAwaits,
		execution:            cfg.Execution,
		executionAuthorities: cfg.ExecutionAuthorities,
		agentIdentities:      cfg.AgentIdentities,
		taskRunRequests:      cfg.TaskRunRequests,
		taskRunRecovery:      cfg.TaskRunRecovery,
		taskRuns:             cfg.TaskRuns,
		taskRunAuthorities:   cfg.TaskRunAuthorities,
		workflowCatalog:      cfg.WorkflowCatalog,
		artifacts:            cfg.Artifacts,
		interactionChat:      cfg.InteractionChat,

		watchPollInterval:      defaultWatchPollInterval,
		watchHeartbeatInterval: defaultWatchHeartbeatInterval,
		watchReconcileInterval: defaultWatchReconcileInterval,

		deliverAssignment: func(
			ctx context.Context,
			st Store,
			workspace,
			leadName string,
		) (driverpkg.AgentMessageDeliveryResult, error) {
			return driverpkg.DeliverLeadAssignmentForDriver(
				ctx,
				cfg.InteractionChat,
				workspace,
				leadName,
			)
		},
	}
	m.ops = map[string]opHandler{
		"claim-ready":                     m.claimReady,
		"claim-task":                      m.claimTask,
		"claim-review":                    m.claimReview,
		"handoff-review":                  m.handoffReview,
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
		"release-review":                  m.releaseReview,
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
	return m
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

func (m *Module) handleVerifyRun(w http.ResponseWriter, r *http.Request) {
	tokenID, ok := m.authenticate(w, r)
	if !ok {
		return
	}
	m.serveAuthorizedOp(w, r, m.verifyRun, tokenID)
}

// driverIdentity is the per-request parent DriverRun identity derived from
// verified token claims.
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
		return 0, fmt.Errorf("parse driver run fencing claim: %w", err)
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
// {op} route and explicitly registered op paths (events/await).
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

type opHandler func(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error)

// verifyParent proves the caller owns a running parent DriverRun.
func (m *Module) verifyParent(ctx context.Context, ws string, id driverIdentity) (*execution.DriverRun, error) {
	run, _, err := m.verifyParentWithOwner(ctx, ws, id)
	return run, err
}

func (m *Module) verifyParentWithOwner(
	ctx context.Context,
	ws string,
	id driverIdentity,
) (*execution.DriverRun, execution.Owner, error) {
	if m.execution == nil || m.executionAuthorities == nil {
		return nil, execution.Owner{},
			fmt.Errorf("execution DriverRun verification capability is unavailable: %w", execution.ErrUnavailable)
	}
	owner, err := driverRunExecutionOwner(id, id.RunID)
	if err != nil {
		return nil, execution.Owner{}, err
	}
	auth, err := m.executionAuthorities.ResolveDriverRunAuthority(ctx, ws, execution.ActionHeartbeatDriverRun, owner)
	if err != nil {
		return nil, execution.Owner{}, err
	}
	run, err := m.execution.HeartbeatDriverRun(ctx, auth, execution.DriverRunHeartbeatCommand{
		WorkspaceKey: ws, Owner: owner, At: time.Now().UTC(),
	})
	if err != nil {
		return nil, execution.Owner{}, err
	}
	return run, owner, nil
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
	run, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	return newVerifiedDriverRunResponse(run)
}

func decodeNoParams(body []byte) error {
	var params struct{}
	err := serverhandler.DecodeOneJSONBytes(body, &params, serverhandler.JSONDecodeOptions{
		MaxBytes:              maxDriverOpBodyBytes,
		DisallowUnknownFields: true,
	})
	if errors.Is(err, serverhandler.ErrTrailingJSON) {
		err = errors.New("multiple JSON values")
	}
	if err != nil {
		return fmt.Errorf("decode driver op params: %s: %w", err.Error(), domain.ErrInvalid)
	}
	return nil
}

func decodeParams[T any](body []byte) (T, error) {
	var params T
	if err := serverhandler.DecodeOneJSONBytes(body, &params, serverhandler.JSONDecodeOptions{
		MaxBytes: maxDriverOpBodyBytes,
	}); err != nil {
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
		Type       string `json:"type"`
		SourceRepo string `json:"sourceRepo"`
		Limit      int    `json:"limit"`
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
	issueBackend, err := m.executionIssueBackend(ws, actor)
	if err != nil {
		return nil, err
	}
	ready, err := driverpkg.ReadyTaskCandidates(ctx, issueBackend, driverpkg.TaskClaimOptions{
		EpicID: epicID, Type: strings.TrimSpace(params.Type),
		SourceRepo: strings.TrimSpace(params.SourceRepo), Limit: params.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list ready tasks: %w", err)
	}
	for _, issue := range ready {
		claimed, claimErr := m.claimDriverRunWorkItem(ctx, ws, id, parent, issue, "")
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
	issueBackend, err := m.executionIssueBackend(ws, actor)
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
	claimed, err := m.claimDriverRunWorkItem(ctx, ws, id, parent, *issue, "")
	if err != nil {
		return nil, fmt.Errorf("claim task: %w", err)
	}
	return claimed, nil
}

// claimReview claims one exact Review-column card without routing it through
// the ready queue. The detail read provides an early, comprehensible conflict;
// RequiredStatus carries the same precondition into FleetDB's atomic claim so a
// concurrent status change cannot turn this into a claim of ordinary ready
// work.
func (m *Module) claimReview(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		TaskID string `json:"taskId"`
	}](body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	taskID := strings.TrimSpace(params.TaskID)
	if taskID == "" {
		return nil, fmt.Errorf("taskId required: %w", domain.ErrInvalid)
	}
	issueBackend, err := m.executionIssueBackend(ws, driverpkg.DriverRunActor(parent.RunID))
	if err != nil {
		return nil, err
	}
	detail, err := issueBackend.Get(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("get review task: %w", err)
	}
	if detail == nil || detail.Status != execution.DriverRunWorkItemRestoreReview {
		return nil, fmt.Errorf("task %q is not in review: %w", taskID, execution.ErrConflict)
	}
	claimed, err := m.claimDriverRunWorkItem(
		ctx, ws, id, parent, detail.IssueData, execution.DriverRunWorkItemRestoreReview,
	)
	if err != nil {
		return nil, fmt.Errorf("claim review task: %w", err)
	}
	return claimed, nil
}

func (m *Module) claimDriverRunWorkItem(
	ctx context.Context,
	ws string,
	id driverIdentity,
	parent *execution.DriverRun,
	issue backend.IssueData,
	requiredStatus string,
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
		WorkspaceKey: ws, RequestID: requestID, Owner: owner, WorkItemID: issue.ID,
		RequiredStatus: requiredStatus, ClaimedAt: time.Now().UTC(),
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
// driverapi reads (verifyParent). The composition root injects the shared
// machine-local prompt reader so this HTTP adapter does not reach through a
// sibling HTTP handler.
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
	if m.rolePrompts == nil {
		return nil, backend.ErrUnavailable(
			"driver-api.role-prompt-reader",
			"role prompt reader is not configured",
			nil,
		)
	}
	role, err := m.store.Roles().Get(ctx, ws, name)
	if err != nil {
		return nil, fmt.Errorf("get role: %w", err)
	}
	return map[string]any{"role": role, "prompt": m.rolePrompts(role)}, nil
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
	issueBackend, err := m.executionIssueBackend(ws, driverpkg.DriverRunActor(parent.RunID))
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
	issueBackend, err := m.executionIssueBackend(ws, driverpkg.DriverRunActor(parent.RunID))
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
	if m.agentIdentities == nil {
		return nil, fmt.Errorf("canonical Agent identity query is unavailable: %w", agents.ErrUnavailable)
	}
	agents, err := m.agentIdentities.ListAgents(ctx, ws, agents.AgentFilter{})
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
	_, owner, err := m.verifyParentWithOwner(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	agentName := strings.TrimSpace(params.Agent)
	parentID := strings.TrimSpace(params.Parent)
	if agentName == "" || parentID == "" {
		return nil, fmt.Errorf("agent and parent required: %w", domain.ErrInvalid)
	}
	if m.agentIdentities == nil || m.execution == nil || m.executionAuthorities == nil {
		return nil, fmt.Errorf("canonical Agent parent binding is unavailable: %w", execution.ErrUnavailable)
	}
	agentIdentity, err := m.agentIdentities.GetAgent(ctx, ws, agentName)
	if err != nil {
		return nil, err
	}
	if agentIdentity == nil || strings.TrimSpace(agentIdentity.ProfileName) == "" {
		return nil, fmt.Errorf("agent %q has no WorkerProfile: %w", agentName, execution.ErrConflict)
	}
	executionAuth, err := m.executionAuthorities.ResolveDriverRunAuthority(ctx, ws, execution.ActionBindWorkerProfileParent, owner)
	if err != nil {
		return nil, err
	}
	expectedParent := strings.TrimSpace(params.ExpectParent)
	updated, err := m.execution.BindWorkerProfileParent(
		ctx,
		executionAuth,
		execution.BindWorkerProfileParentCommand{
			WorkspaceKey:   ws,
			RequestID:      "bind-worker-profile-parent:" + owner.ResourceID + ":" + agentName,
			ProfileID:      agentIdentity.ProfileName,
			ExpectedParent: expectedParent,
			Parent:         parentID,
			Owner:          owner,
		},
	)
	if err != nil {
		return nil, err
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
	parent, owner, err := m.verifyParentWithOwner(ctx, ws, id)
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
		auth, err := m.executionAuthorities.ResolveDriverRunAuthority(ctx, ws, execution.ActionEnqueueLeadAssignment, owner)
		if err != nil {
			return nil, err
		}
		if _, err := m.execution.EnqueueLeadAssignment(ctx, auth, execution.EnqueueLeadAssignmentCommand{
			WorkspaceKey: ws,
			EpicID:       firstNonEmpty(parent.EpicID, driverpkg.DriverRunPayloadEpicID(parent.Payload)),
			TargetAgent:  leadName,
			Owner:        owner,
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
	result, err := driverpkg.DeliverAgentMessageForDriver(
		ctx,
		m.interactionChat,
		ws,
		parent.RunID,
		agentName,
		message,
	)
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
