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
	// Empty keeps runners on the legacy direct-fleet-db env.
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
}

// Module serves the workspace-scoped driver-op routes.
type Module struct {
	store            store.Store
	apiToken         string
	runTokenKey      []byte
	apiBaseURL       string
	worktreePath     string
	localSettingsDir string
	issueBackends    IssueBackendFactory
	dispatcher       *connector.Dispatcher
	ops              map[string]opHandler

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
		store:            cfg.Store,
		apiToken:         strings.TrimSpace(cfg.APIToken),
		runTokenKey:      cfg.RunTokenKey,
		apiBaseURL:       strings.TrimSpace(cfg.APIBaseURL),
		worktreePath:     cfg.WorktreePath,
		localSettingsDir: strings.TrimSpace(cfg.LocalSettingsDir),
		issueBackends:    cfg.IssueBackends,
		dispatcher:       cfg.Dispatcher,

		watchPollInterval:      defaultWatchPollInterval,
		watchHeartbeatInterval: defaultWatchHeartbeatInterval,
		watchReconcileInterval: defaultWatchReconcileInterval,

		deliverAssignment: leadcontrol.DeliverCurrentAssignment,

		internalEvents: &trigger.InternalSource{Store: cfg.Store},
	}
	m.ops = map[string]opHandler{
		"claim-ready":                 m.claimReady,
		"claim-task":                  m.claimTask,
		"binding-config":              m.bindingConfig,
		"role-get":                    m.roleGet,
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
		"task-diff":                   m.taskDiff,
		"release-task":                m.releaseTask,
		"connector-dispatch":          m.connectorDispatch,
		"emit-event":                  m.emitEvent,
		"issue-get":                   m.issueGet,
		"issue-list":                  m.issueList,
		"issue-list-comments":         m.issueListComments,
		"issue-comment":               m.issueComment,
		"issue-update":                m.issueUpdate,
		"issue-add-label":             m.issueAddLabel,
		"issue-remove-label":          m.issueRemoveLabel,
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
	// Await-event ops (AW9): two-segment paths the {op} pattern cannot match.
	mux.HandleFunc("POST /api/workspaces/{ws}/driver/events/await", m.handleAwaitEvent)
	mux.HandleFunc("GET /api/workspaces/{ws}/driver/events/awaits", m.handleListAwaits)
	// Composition ops (AW10): same two-segment-path situation.
	mux.HandleFunc("POST /api/workspaces/{ws}/driver/workflows/start", m.handleWorkflowsStart)
	mux.HandleFunc("POST /api/workspaces/{ws}/driver/workflows/await", m.handleWorkflowsAwait)
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
		RunID:   strings.TrimSpace(r.Header.Get(HeaderDriverRunID)),
		NodeID:  strings.TrimSpace(r.Header.Get(HeaderDriverNodeID)),
		LeaseID: strings.TrimSpace(r.Header.Get(HeaderDriverLeaseID)),
		fence:   r.Header.Get(HeaderDriverFencingToken),
	}
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
	// ClaimReadyTask defaults a non-positive limit itself.
	claimed, err := driverpkg.ClaimReadyTask(ctx, issueBackend, driverpkg.TaskClaimOptions{
		EpicID: epicID,
		Actor:  actor,
		Type:   strings.TrimSpace(params.Type),
		Limit:  params.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("claim ready task: %w", err)
	}
	return claimed, nil
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
	claimed, err := driverpkg.ClaimTask(ctx, issueBackend, driverpkg.TaskClaimByIDOptions{
		TaskID: params.TaskID,
		Actor:  actor,
		EpicID: strings.TrimSpace(params.EpicID),
		Limit:  params.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("claim task: %w", err)
	}
	return claimed, nil
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
	Runner             string   `json:"runner"`
	ProviderProfile    string   `json:"providerProfile"`
	ParentSessionID    string   `json:"parentSessionId"`
	NodeID             string   `json:"nodeId"`
	TargetNodeID       string   `json:"targetNodeId"`
	RunnerID           string   `json:"runnerId"`
	LeaseToken         string   `json:"leaseToken"`
	SupportedProviders []string `json:"supportedProviders"`
	Capabilities       []string `json:"capabilities"`
	RepoRef            string   `json:"repoRef"`
	SandboxPlacement   struct {
		Provider  string `json:"provider"`
		SandboxID string `json:"sandboxId"`
		CWD       string `json:"cwd"`
		RepoRef   string `json:"repoRef"`
	} `json:"sandboxPlacement"`
	DeferCompletion bool `json:"deferCompletion"`
	EnqueueOnly     bool `json:"enqueueOnly"`
	// CloseTask optionally overrides whether the serve task worker closes the
	// underlying task issue on success. Pointer so an absent field preserves the
	// worker default (true) byte-for-byte; a planner run passes false to leave
	// the card in design+review. Precedent: taskrunapi completeParams.CloseTask.
	CloseTask *bool `json:"closeTask,omitempty"`
	// Input is the optional task-run payload (camelCase driver wire). It is
	// persisted on the run and delivered verbatim to the runner.
	Input json.RawMessage `json:"input,omitempty"`
}

func (p execTaskParams) requestOptions(ws string, id driverIdentity, fencingToken int64) driverpkg.TaskRunRequestOptions {
	opts := driverpkg.TaskRunRequestOptions{
		WorkspaceKey:       ws,
		DriverRunID:        id.RunID,
		DriverStepID:       p.DriverStepID,
		TaskRunID:          p.TaskRunID,
		TaskID:             p.TaskID,
		WorkerProfileID:    p.WorkerProfileID,
		Runner:             p.Runner,
		ProviderProfile:    p.ProviderProfile,
		ParentSessionID:    p.ParentSessionID,
		ParentNodeID:       id.NodeID,
		ParentLeaseID:      id.LeaseID,
		ParentFence:        fencingToken,
		NodeID:             firstNonEmpty(p.NodeID, p.TargetNodeID),
		RunnerID:           p.RunnerID,
		LeaseToken:         p.LeaseToken,
		SupportedProviders: p.SupportedProviders,
		Capabilities:       p.Capabilities,
		DeferCompletion:    p.DeferCompletion,
		CloseTaskOnSuccess: p.CloseTask,
		Input:              p.Input,
		SandboxPlacement: domain.TaskRunPlacement{
			Provider:  p.SandboxPlacement.Provider,
			SandboxID: p.SandboxPlacement.SandboxID,
			CWD:       p.SandboxPlacement.CWD,
			RepoRef:   firstNonEmpty(p.SandboxPlacement.RepoRef, p.RepoRef),
		},
	}
	if strings.TrimSpace(p.Runner) == "" {
		opts.ProviderProfile = p.ProviderProfile
		opts.SupportedProviders = p.SupportedProviders
	}
	return opts
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
	// Auto-create the run→task-run linkage step when the caller supplied none.
	// The DriverStep is the STRUCTURED edge the unified agent detail resolves a
	// run's transcript through (getRun embeds steps; the workflow's own JSON
	// result is buried as a string in output.flue_stdout_tail, so without a
	// step a bare exec-task dispatch is invisible to the UI). fleet-db requires
	// a client-minted step_id and fences creation to the run's owner, so the
	// id is deterministic per (run, task) — a durable-resume re-dispatch hits
	// already-exists and REUSES the same step instead of duplicating it.
	// Best-effort beyond that: a step-create failure must never block the
	// dispatch — the linkage degrades, the work proceeds.
	if strings.TrimSpace(opts.DriverStepID) == "" {
		stepID := "step-" + id.RunID + "-" + strings.TrimSpace(params.TaskID)
		_, stepErr := m.store.DriverSteps().CreateForRun(ctx, ws, id.RunID, store.DriverStepCreate{
			StepID:       stepID,
			StepKind:     "task_run",
			Status:       domain.DriverStepQueued,
			NodeID:       id.NodeID,
			LeaseID:      id.LeaseID,
			FencingToken: fencingToken,
		})
		if stepErr == nil || errors.Is(stepErr, domain.ErrConflict) || errors.Is(stepErr, domain.ErrAlreadyExists) {
			opts.DriverStepID = stepID
		}
	}
	executor := driverpkg.HostBridgeTaskExecutor{
		Store:            m.store,
		WorktreePath:     m.worktreePath,
		APIBaseURL:       m.apiBaseURL,
		LocalSettingsDir: m.localSettingsDir,
		WorktreeResolver: driverpkg.LocalTaskWorktreeResolver{Store: m.store, Lineage: driverpkg.DefaultStackLineageLookup()},
		StackStore:       driverpkg.DefaultStackStore(),
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
		// Actor: accepted for wire-compat, IGNORED. SECURITY: the release
		// ownership check is keyed by the server-derived run actor below, never
		// by caller input — otherwise a run could present a victim's actor and
		// release a lock it never held (cross-agent task theft). The claim path
		// derives the SAME actor from the SAME run, so a run releases exactly
		// the leases it took; cross-run recovery relies on lock TTL, not on a
		// caller-supplied actor.
		Actor string `json:"actor"`
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
	result, err := driverpkg.ReleaseTask(ctx, issueBackend, driverpkg.TaskReleaseOptions{
		TaskID: params.TaskID,
		Actor:  actor,
	})
	if err != nil {
		return nil, fmt.Errorf("release task: %w", err)
	}
	return result, nil
}

// --- issue (card) ops (P0-1): thin IssueBackend pass-throughs so a workflow can
// read and mutate a fleet-db card. Auth is the run token via verifyParent; the
// actor is derived from the parent run (as in claimReady/releaseTask). The
// loom.issue.* SDK surface is generated from sdk/op-spec.mjs.

func (m *Module) issueBackendForRun(ctx context.Context, ws string, id driverIdentity) (backend.IssueBackend, string, error) {
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, "", err
	}
	actor := driverpkg.DriverRunActor(parent.RunID)
	issueBackend, err := m.issueBackends(ws, actor)
	if err != nil {
		return nil, "", err
	}
	return issueBackend, actor, nil
}

func (m *Module) issueGet(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		IssueID string `json:"issueId"`
	}](body)
	if err != nil {
		return nil, err
	}
	issueBackend, _, err := m.issueBackendForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.IssueID) == "" {
		return nil, fmt.Errorf("issueId required: %w", domain.ErrInvalid)
	}
	return issueBackend.Get(ctx, params.IssueID)
}

func (m *Module) issueList(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		ExternalRef string `json:"externalRef"`
		Type        string `json:"type"`
		Status      string `json:"status"`
		Limit       int    `json:"limit"`
	}](body)
	if err != nil {
		return nil, err
	}
	issueBackend, _, err := m.issueBackendForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	// Same default cap as activeTaskRuns: an omitted limit must not turn into
	// an unbounded fetch of every matching issue in the workspace.
	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	return issueBackend.List(ctx, backend.ListOpts{
		ExternalRef: params.ExternalRef,
		IssueType:   params.Type,
		Status:      params.Status,
		Limit:       limit,
	})
}

func (m *Module) issueListComments(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		IssueID string `json:"issueId"`
	}](body)
	if err != nil {
		return nil, err
	}
	issueBackend, _, err := m.issueBackendForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.IssueID) == "" {
		return nil, fmt.Errorf("issueId required: %w", domain.ErrInvalid)
	}
	return issueBackend.ListComments(ctx, params.IssueID)
}

func (m *Module) issueComment(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		IssueID string `json:"issueId"`
		Body    string `json:"body"`
	}](body)
	if err != nil {
		return nil, err
	}
	issueBackend, actor, err := m.issueBackendForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.IssueID) == "" || strings.TrimSpace(params.Body) == "" {
		return nil, fmt.Errorf("issueId and body required: %w", domain.ErrInvalid)
	}
	return issueBackend.AddComment(ctx, backend.CommentAddParams{
		IssueID: params.IssueID,
		Author:  actor,
		Text:    params.Body,
	})
}

func (m *Module) issueUpdate(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		IssueID     string   `json:"issueId"`
		Status      *string  `json:"status"`
		Priority    *int     `json:"priority"`
		Labels      []string `json:"labels"`
		Assignee    *string  `json:"assignee"`
		ExternalRef *string  `json:"externalRef"`
	}](body)
	if err != nil {
		return nil, err
	}
	issueBackend, _, err := m.issueBackendForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.IssueID) == "" {
		return nil, fmt.Errorf("issueId required: %w", domain.ErrInvalid)
	}
	update := backend.UpdateParams{
		Status:      params.Status,
		Priority:    params.Priority,
		Assignee:    params.Assignee,
		ExternalRef: params.ExternalRef,
	}
	if params.Labels != nil {
		update.SetLabels = params.Labels
	}
	if err := issueBackend.Update(ctx, params.IssueID, update); err != nil {
		return nil, fmt.Errorf("update issue: %w", err)
	}
	// Light ack — avoid re-fetching the full detail projection (Get fans out to
	// issue + deps + comments); callers that need the card fetch it explicitly.
	return map[string]any{"id": params.IssueID}, nil
}

func (m *Module) issueAddLabel(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	return m.issueLabelOp(ctx, ws, id, body, true)
}

func (m *Module) issueRemoveLabel(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	return m.issueLabelOp(ctx, ws, id, body, false)
}

func (m *Module) issueLabelOp(ctx context.Context, ws string, id driverIdentity, body []byte, add bool) (any, error) {
	params, err := decodeParams[struct {
		IssueID string `json:"issueId"`
		Label   string `json:"label"`
	}](body)
	if err != nil {
		return nil, err
	}
	issueBackend, _, err := m.issueBackendForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.IssueID) == "" || strings.TrimSpace(params.Label) == "" {
		return nil, fmt.Errorf("issueId and label required: %w", domain.ErrInvalid)
	}
	if add {
		err = issueBackend.AddLabel(ctx, params.IssueID, params.Label)
	} else {
		err = issueBackend.RemoveLabel(ctx, params.IssueID, params.Label)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": params.IssueID, "label": params.Label}, nil
}

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

// writeDomainOpError maps domain sentinel errors onto the structured error
// envelope. Defaults to a non-retryable internal error: only transient
// classes (timeouts, cancellation) advertise retryability.
func writeDomainOpError(w http.ResponseWriter, err error) {
	if writeConnectorOpError(w, err) {
		return
	}
	if writeAwaitOpError(w, err) {
		return
	}
	var coded *codedOpError
	if errors.As(err, &coded) {
		writeOpErrorDetails(w, coded.status, coded.code, coded.Error(), coded.retryable, coded.details)
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
