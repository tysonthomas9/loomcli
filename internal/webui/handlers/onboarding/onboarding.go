// Package onboarding implements the GET /api/onboarding/status and
// GET /api/workspaces/{ws}/onboarding/status endpoints. The package
// computes new-user onboarding step state from existing services and
// keeps that computation as a pure function (ComputeStatus) so it can be
// reused by a future `loom doctor` CLI without going through HTTP.
//
// See docs/product/web-onboarding-spec.md for the full contract.
package onboarding

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// StepID identifies an onboarding step in the wire format. The values
// are part of the API contract — do not rename without updating the
// frontend stepRegistry.
type StepID string

const (
	StepWorkspaceRepo StepID = "workspace-repo"
	StepVerifyRepo    StepID = "verify-repo"
	StepSetupBackend  StepID = "setup-backend"
	StepCreateAgent   StepID = "create-agent"
	StepCreateIssue   StepID = "create-issue"
	StepRunAgent      StepID = "run-agent"
)

// StepStatus is the lifecycle state of a single step.
//
// Only `complete` and `warning` unblock downstream steps. `actionable`
// signals the step is reachable now (replaces the older `pending`
// vocabulary). `unknown` is used when a server dependency is unavailable
// rather than concluding the step is blocked.
type StepStatus string

const (
	StatusComplete   StepStatus = "complete"
	StatusActionable StepStatus = "actionable"
	StatusBlocked    StepStatus = "blocked"
	StatusWarning    StepStatus = "warning"
	StatusError      StepStatus = "error"
	StatusUnknown    StepStatus = "unknown"
)

// Action names map to OnboardingActionsContext on the frontend. Keep in
// sync with src/types/onboarding.ts.
const (
	ActionOpenWorkspaceRepoWizard = "open_workspace_repo_wizard"
	ActionOpenRepoChecks          = "open_repo_checks"
	ActionOpenBackendSetup        = "open_backend_setup"
	ActionOpenCreateAgent         = "open_create_agent"
	ActionOpenCreateIssue         = "open_create_issue"
	ActionStartFirstAgent         = "start_first_agent"
)

// Step is the wire shape of a single onboarding step.
type Step struct {
	ID      StepID     `json:"id"`
	Status  StepStatus `json:"status"`
	Action  string     `json:"action"`
	Message string     `json:"message,omitempty"`
}

// Status is the wire shape returned by the onboarding endpoints.
type Status struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	ActiveRepo  string `json:"active_repo,omitempty"`
	Steps       []Step `json:"steps"`
	AllComplete bool   `json:"all_complete"`
}

// Deps is the interface ComputeStatus depends on. The real wiring
// (server.go) supplies an adapter; tests supply a stub.
type Deps interface {
	// HasAnyWorkspace returns true when at least one workspace is
	// configured. Used to evaluate step 1 in the no-workspace case.
	HasAnyWorkspace(ctx context.Context) (bool, error)
	// GetWorkspace returns the workspace topology (repos, agents, ...)
	// for a known workspace ID. Should return a non-nil error when the
	// workspace does not exist.
	GetWorkspace(ctx context.Context, wsID string) (*ops.WorkspaceData, error)
	// BackendsHealth lists registered AI backends with health metadata.
	BackendsHealth(ctx context.Context) ([]ops.BackendHealth, error)
	// IssueCount returns the number of issues in the workspace context
	// carried by ctx (workspace ID is injected upstream by middleware).
	IssueCount(ctx context.Context) (int, error)
}

// ComputeStatus is a pure function over Deps. workspaceID is the empty
// string for the no-workspace (top-level) endpoint.
func ComputeStatus(ctx context.Context, deps Deps, workspaceID string) Status {
	if workspaceID == "" {
		return computeNoWorkspace(ctx, deps)
	}
	return computeWorkspaceScoped(ctx, deps, workspaceID)
}

func computeNoWorkspace(ctx context.Context, deps Deps) Status {
	any, err := deps.HasAnyWorkspace(ctx)
	step1 := Step{ID: StepWorkspaceRepo, Action: ActionOpenWorkspaceRepoWizard}
	switch {
	case err != nil:
		step1.Status = StatusError
		step1.Message = "could not list workspaces"
	case any:
		// A workspace exists but the caller is on the top-level route;
		// frontend should redirect to the workspace-scoped endpoint.
		step1.Status = StatusComplete
	default:
		step1.Status = StatusActionable
	}

	steps := []Step{
		step1,
		{ID: StepVerifyRepo, Status: StatusBlocked, Action: ActionOpenRepoChecks},
		{ID: StepSetupBackend, Status: StatusBlocked, Action: ActionOpenBackendSetup},
		{ID: StepCreateAgent, Status: StatusBlocked, Action: ActionOpenCreateAgent},
		{ID: StepCreateIssue, Status: StatusBlocked, Action: ActionOpenCreateIssue},
		{ID: StepRunAgent, Status: StatusBlocked, Action: ActionStartFirstAgent},
	}
	return Status{Steps: steps, AllComplete: false}
}

func computeWorkspaceScoped(ctx context.Context, deps Deps, wsID string) Status {
	steps := make([]Step, 0, 6)

	ws, wsErr := deps.GetWorkspace(ctx, wsID)
	steps = append(steps, evalWorkspaceRepoStep(ws, wsErr))

	activeRepo := firstRepoName(ws)

	steps = append(steps, evalVerifyRepoStep(ws, wsErr, prevUnblocks(steps)))

	backendsStatus, backendsMsg := evalSetupBackend(ctx, deps, prevUnblocks(steps))
	steps = append(steps, Step{ID: StepSetupBackend, Status: backendsStatus, Action: ActionOpenBackendSetup, Message: backendsMsg})

	steps = append(steps, evalCreateAgentStep(ws, prevUnblocks(steps)))
	steps = append(steps, evalCreateIssueStep(ctx, deps, prevUnblocks(steps)))
	steps = append(steps, evalRunAgentStep(prevUnblocks(steps)))

	return Status{
		WorkspaceID: wsID,
		ActiveRepo:  activeRepo,
		Steps:       steps,
		AllComplete: allComplete(steps),
	}
}

// prevUnblocks returns true when every prior step has a status that
// unblocks the next one (complete or warning).
func prevUnblocks(steps []Step) bool {
	for _, s := range steps {
		if s.Status != StatusComplete && s.Status != StatusWarning {
			return false
		}
	}
	return true
}

func allComplete(steps []Step) bool {
	for _, s := range steps {
		if s.Status != StatusComplete && s.Status != StatusWarning {
			return false
		}
	}
	return true
}

func evalWorkspaceRepoStep(ws *ops.WorkspaceData, wsErr error) Step {
	step := Step{ID: StepWorkspaceRepo, Action: ActionOpenWorkspaceRepoWizard}
	switch {
	case wsErr != nil:
		step.Status = StatusError
		step.Message = "workspace lookup failed"
	case ws == nil || len(ws.Repos) == 0:
		step.Status = StatusActionable
		step.Message = "Workspace has no repos yet."
	default:
		step.Status = StatusComplete
	}
	return step
}

func evalVerifyRepoStep(ws *ops.WorkspaceData, wsErr error, unblocked bool) Step {
	step := Step{ID: StepVerifyRepo, Action: ActionOpenRepoChecks}
	if !unblocked {
		step.Status = StatusBlocked
		return step
	}
	if wsErr != nil || ws == nil || len(ws.Repos) == 0 {
		step.Status = StatusBlocked
		return step
	}
	// Phase 1: deeper repo verification (path exists, is git repo, has
	// default branch, ...) is staged for Phase 2 wiring. For now, the
	// presence of a repo with a default branch is treated as complete;
	// missing default branch surfaces as warning, never as error.
	repo := ws.Repos[0]
	if repo.DefaultBranch == "" {
		step.Status = StatusWarning
		step.Message = "Repo has no detected default branch; first local run is still available."
		return step
	}
	step.Status = StatusComplete
	return step
}

func evalSetupBackend(ctx context.Context, deps Deps, unblocked bool) (StepStatus, string) {
	if !unblocked {
		return StatusBlocked, ""
	}
	healths, err := deps.BackendsHealth(ctx)
	if err != nil {
		return StatusUnknown, "Could not read backend health."
	}
	if len(healths) == 0 {
		return StatusActionable, "No AI backends are registered yet."
	}
	// `Available` is the canonical "ready to use" signal from each
	// backend's HealthCheck. Some backends (e.g. opencode) report
	// Available=true without APIKeySet because they don't need a Loom-
	// owned env var. Trust the backend, not a synthetic two-field check.
	for _, h := range healths {
		if h.Available {
			return StatusComplete, ""
		}
	}
	// No ready backend. Pick the most informative actionable backend
	// to surface in the message — installed-but-no-key is the common
	// case the user can act on immediately.
	for _, h := range healths {
		if h.Installed && !h.APIKeySet {
			return StatusActionable, h.DisplayName + " is installed but authentication is missing."
		}
	}
	for _, h := range healths {
		if h.Installed && h.Message != "" {
			return StatusActionable, h.DisplayName + ": " + h.Message
		}
	}
	return StatusActionable, "No backend is installed and ready yet."
}

func evalCreateAgentStep(ws *ops.WorkspaceData, unblocked bool) Step {
	step := Step{ID: StepCreateAgent, Action: ActionOpenCreateAgent}
	if !unblocked {
		step.Status = StatusBlocked
		return step
	}
	if ws == nil || len(ws.Agents) == 0 {
		step.Status = StatusActionable
		return step
	}
	step.Status = StatusComplete
	return step
}

func evalCreateIssueStep(ctx context.Context, deps Deps, unblocked bool) Step {
	step := Step{ID: StepCreateIssue, Action: ActionOpenCreateIssue}
	if !unblocked {
		step.Status = StatusBlocked
		return step
	}
	count, err := deps.IssueCount(ctx)
	if err != nil {
		step.Status = StatusUnknown
		step.Message = "Could not count issues."
		return step
	}
	if count > 0 {
		step.Status = StatusComplete
		return step
	}
	step.Status = StatusActionable
	return step
}

func evalRunAgentStep(unblocked bool) Step {
	step := Step{ID: StepRunAgent, Action: ActionStartFirstAgent}
	if !unblocked {
		step.Status = StatusBlocked
		return step
	}
	// Phase 1: detecting "an agent has run" requires the run/session
	// inspection plumbing landed in Phase 6. Until then, this step is
	// always actionable once unblocked, and the frontend treats a
	// successful "start agent" call as the implicit completion signal.
	step.Status = StatusActionable
	return step
}

func firstRepoName(ws *ops.WorkspaceData) string {
	if ws == nil || len(ws.Repos) == 0 {
		return ""
	}
	return ws.Repos[0].Name
}

type response struct {
	Success bool   `json:"success"`
	Data    Status `json:"data"`
	Error   string `json:"error,omitempty"`
}

// HandleStatus serves both the top-level (no workspace) and the
// workspace-scoped onboarding endpoints. The workspace ID is read from
// the request context (injected by the workspace path-value middleware)
// when present; otherwise the no-workspace branch runs.
func HandleStatus(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		s := ComputeStatus(r.Context(), deps, wsID)
		handler.WriteJSON(w, http.StatusOK, response{Success: true, Data: s})
	}
}

// IssueBackendCounter adapts a per-request IssueBackend resolver into the
// IssueCount(ctx) shape Deps requires. Real server wiring constructs one
// of these and embeds it in the Deps adapter.
type IssueBackendCounter struct {
	Resolve func(ctx context.Context) backend.IssueBackend
}

// Count returns the issue count, or 0 with a nil error when the resolver
// returns nil (e.g. fleet-db is unconfigured for this server) so callers
// can treat that as "no issues yet" rather than an error.
func (c IssueBackendCounter) Count(ctx context.Context) (int, error) {
	if c.Resolve == nil {
		return 0, nil
	}
	ib := c.Resolve(ctx)
	if ib == nil {
		return 0, nil
	}
	return ib.Count(ctx, backend.CountOpts{})
}
