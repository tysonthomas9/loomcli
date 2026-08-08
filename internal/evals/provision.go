package evals

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

const (
	DefaultEvalCronSchedule = "0 * * * *"
	EvalCronRouteKey        = "cron.session-eval-agent"
	EvalCronBindingID       = "binding-cron-session-eval-agent"
)

type EnsureResult struct {
	Action          string `json:"action"`
	BindingID       string `json:"binding_id"`
	RouteKey        string `json:"route_key"`
	Schedule        string `json:"schedule"`
	Enabled         bool   `json:"enabled"`
	DriverID        string `json:"driver_id"`
	DriverVersionID string `json:"driver_version_id"`
	WorkflowName    string `json:"workflow_name"`
}

// EnsureEvalCron explicitly opts a workspace into session evaluation. The
// session-eval-agent bundle carries both the orchestrator and the derived
// session-eval-task-runner sibling, so ensuring the single builtin workflow
// satisfies the "both eval workflows" provisioning requirement.
// ensureEvalWorkflowVersion materializes the builtin session-eval workflow and
// resolves its driver id plus the currently active driver version — the pin
// target for both create and re-pin paths of the cron binding.
func ensureEvalWorkflowVersion(ctx context.Context, st store.Store, ws string) (driverID, versionID string, err error) {
	if err := workflowdefs.EnsureBuiltinWorkflow(ctx, st, ws, workflowdefs.BuiltinSessionEvalAgentWorkflowName); err != nil {
		return "", "", fmt.Errorf("ensure session eval builtin workflow: %w", err)
	}
	driverID, err = workflowdefs.ResolveDriverID(ctx, st, ws, workflowdefs.BuiltinSessionEvalAgentWorkflowName)
	if err != nil {
		return "", "", fmt.Errorf("resolve session eval builtin workflow: %w", err)
	}
	driver, err := st.Drivers().Get(ctx, ws, driverID)
	if err != nil {
		return "", "", fmt.Errorf("load session eval workflow driver: %w", err)
	}
	versionID = strings.TrimSpace(driver.ActiveVersionID)
	if versionID == "" {
		return "", "", fmt.Errorf("session eval workflow %q has no active version: %w", driver.DriverID, domain.ErrInvalid)
	}
	return driver.DriverID, versionID, nil
}

func EnsureEvalCron(ctx context.Context, st store.Store, ws string, schedule string) (EnsureResult, error) {
	if st == nil {
		return EnsureResult{}, fmt.Errorf("store is required: %w", domain.ErrInvalid)
	}
	schedule = strings.TrimSpace(schedule)
	if schedule == "" {
		schedule = DefaultEvalCronSchedule
	}
	if err := trigger.ValidateSchedule(schedule); err != nil {
		return EnsureResult{}, err
	}
	driverID, versionID, err := ensureEvalWorkflowVersion(ctx, st, ws)
	if err != nil {
		return EnsureResult{}, err
	}

	existing, err := st.TriggerBindings().GetByRouteKey(ctx, ws, EvalCronRouteKey)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return EnsureResult{}, fmt.Errorf("load session eval cron binding: %w", err)
		}
		return createEvalCronBinding(ctx, st, ws, driverID, versionID, schedule)
	}

	driverIDPatch := driverID
	versionIDPatch := versionID
	sourceKind := trigger.CronSourceKind
	routeKey := EvalCronRouteKey
	updated, err := st.TriggerBindings().Update(ctx, ws, existing.BindingID, store.TriggerBindingUpdate{
		SourceKind:      &sourceKind,
		RouteKey:        &routeKey,
		DriverID:        &driverIDPatch,
		DriverVersionID: &versionIDPatch,
		Schedule:        &schedule,
	})
	if err != nil {
		return EnsureResult{}, fmt.Errorf("update session eval cron binding: %w", err)
	}
	return ensureResult("updated", updated), nil
}

// createEvalCronBinding provisions the first cron binding for a workspace;
// Enabled:true is the operator's opt-in act (LOOMCLI-58).
func createEvalCronBinding(ctx context.Context, st store.Store, ws, driverID, versionID, schedule string) (EnsureResult, error) {
	created, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey:    ws,
		BindingID:       EvalCronBindingID,
		Name:            "Session eval agent",
		SourceKind:      trigger.CronSourceKind,
		RouteKey:        EvalCronRouteKey,
		DriverID:        driverID,
		DriverVersionID: versionID,
		Schedule:        schedule,
		Enabled:         true,
	})
	if err != nil {
		return EnsureResult{}, fmt.Errorf("create session eval cron binding: %w", err)
	}
	return ensureResult("created", created), nil
}

func ensureResult(action string, binding *domain.TriggerBinding) EnsureResult {
	out := EnsureResult{Action: action, WorkflowName: workflowdefs.BuiltinSessionEvalAgentWorkflowName}
	if binding == nil {
		return out
	}
	out.BindingID = binding.BindingID
	out.RouteKey = binding.RouteKey
	out.Schedule = binding.Schedule
	out.Enabled = binding.Enabled
	out.DriverID = binding.DriverID
	out.DriverVersionID = binding.DriverVersionID
	return out
}
