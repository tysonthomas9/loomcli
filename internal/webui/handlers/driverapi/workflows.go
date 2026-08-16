// Composition driver ops (ARCHITECTURE-PROPOSAL §7 step 8, chunk AW10).
//
// POST /api/workspaces/{ws}/driver/workflows/start — create (or idempotently
// re-read) the deterministic child run of the verified caller: re-entry
// re-issuing the same start gets the same childRunId back, never a duplicate.
//
// POST /api/workspaces/{ws}/driver/workflows/await — await-machinery sugar
// over pattern "run.finished:{childRunId}": validates the child belongs to
// the caller, then runs the standard events/await flow (same suspended /
// satisfied / timed_out wire shapes) consuming a normal awaitIndex slot. The
// satisfied response additionally carries the child's terminal status,
// summary and errorClass so a resumed parent branches without a second fetch.
//
// Authentication is the standard run-scoped driver-op pipeline; wire shapes
// are camelCase per the SDK v2 convention.
package driverapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// workflowsStartParams is the camelCase workflows/start request body.
type workflowsStartParams struct {
	// WorkflowName names the registered workflow (driver) to run.
	WorkflowName string `json:"workflowName"`
	// Input is the child's initial payload; empty means {}.
	Input json.RawMessage `json:"input,omitempty"`
	// IdempotencyKey keys the deterministic child identity. When absent,
	// StartIndex (the SDK's 1-based per-run start counter) is required.
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	StartIndex     int    `json:"startIndex,omitempty"`
}

// workflowsStartResponse reports the (possibly replayed) child run.
type workflowsStartResponse struct {
	ChildRunID   string `json:"childRunId"`
	WorkflowName string `json:"workflowName"`
	Status       string `json:"status"`
	ParentRunID  string `json:"parentRunId"`
}

func normalizedWorkflowStartPayload(input json.RawMessage) (json.RawMessage, error) {
	payload := append(json.RawMessage(nil), input...)
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if !json.Valid(payload) {
		return nil, fmt.Errorf("child input must be valid JSON: %w", persistence.ErrInvalid)
	}
	return payload, nil
}

func workflowStartOwner(parent *execution.DriverRun, id driverIdentity) (execution.Owner, error) {
	fence, err := id.FencingToken()
	if err != nil {
		return execution.Owner{}, err
	}
	return execution.Owner{
		ResourceKind: execution.ResourceDriverRun, ResourceID: parent.RunID,
		NodeID: id.NodeID, LeaseID: id.LeaseID, LeaseToken: id.LeaseToken, FencingToken: fence,
	}, nil
}

func decodeWorkflowsStartParams(body []byte) (workflowsStartParams, string, error) {
	params, err := decodeParams[workflowsStartParams](body)
	if err != nil {
		return workflowsStartParams{}, "", err
	}
	workflowName := strings.TrimSpace(params.WorkflowName)
	if workflowName == "" {
		return workflowsStartParams{}, "", fmt.Errorf("workflowName required: %w", persistence.ErrInvalid)
	}
	return params, workflowName, nil
}

// workflowsStart is the workflows/start handler.
func (m *Module) workflowsStart(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, workflowName, err := decodeWorkflowsStartParams(body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if m.execution == nil || m.executionAuthorities == nil {
		return nil, fmt.Errorf("execution child-run API is unavailable: %w", execution.ErrUnavailable)
	}
	childKey, err := driverpkg.ResolveChildWorkflowStartKey(params.IdempotencyKey, params.StartIndex)
	if err != nil {
		return nil, err
	}
	target, version, err := m.resolveWorkflowStartTarget(ctx, ws, workflowName)
	if err != nil {
		return nil, err
	}
	payload, err := normalizedWorkflowStartPayload(params.Input)
	if err != nil {
		return nil, err
	}
	owner, err := workflowStartOwner(parent, id)
	if err != nil {
		return nil, err
	}
	auth, err := m.executionAuthorities.ResolveDriverRunAuthority(ctx, ws, execution.ActionStartChildDriverRun, owner)
	if err != nil {
		return nil, err
	}
	childSnapshot, err := m.execution.StartChildDriverRun(ctx, auth, execution.StartChildDriverRunCommand{
		WorkspaceKey: ws, RequestID: execution.ChildDriverRunRequestID(parent.RunID, childKey), Owner: owner,
		ChildKey: childKey, DriverID: target.DriverID, DriverVersionID: version.VersionID,
		Payload: payload, MaxDepth: driverpkg.ResolveCompositionMaxDepth(),
	})
	if err != nil {
		return nil, err
	}
	return workflowsStartResponse{
		ChildRunID:   childSnapshot.RunID,
		WorkflowName: childSnapshot.DriverID,
		Status:       string(childSnapshot.Status),
		ParentRunID:  childSnapshot.ParentRunID,
	}, nil
}

func (m *Module) resolveWorkflowStartTarget(
	ctx context.Context,
	workspace, workflowName string,
) (*workflowcatalog.Driver, *workflowcatalog.DriverVersion, error) {
	if m.workflowCatalog == nil {
		return nil, nil, fmt.Errorf("workflow catalog query API is unavailable: %w", execution.ErrUnavailable)
	}
	target, err := m.workflowCatalog.GetDriver(ctx, workspace, workflowName)
	if err != nil {
		return nil, nil, err
	}
	if target == nil {
		return nil, nil, fmt.Errorf("workflow %q was not found: %w", workflowName, persistence.ErrNotFound)
	}
	if strings.TrimSpace(target.ActiveVersionID) == "" {
		return nil, nil, fmt.Errorf("workflow %q has no active version: %w", workflowName, persistence.ErrInvalid)
	}
	version, err := m.workflowCatalog.GetVersion(ctx, workspace, target.ActiveVersionID)
	if err != nil {
		return nil, nil, err
	}
	if version == nil || version.DriverID != target.DriverID || version.ValidationStatus != workflowcatalog.DriverVersionValidationPassed {
		return nil, nil, fmt.Errorf("workflow %q active version %q is not passed: %w", workflowName, target.ActiveVersionID, persistence.ErrInvalid)
	}
	return target, version, nil
}

// workflowsAwaitParams is the camelCase workflows/await request body.
type workflowsAwaitParams struct {
	ChildRunID string `json:"childRunId"`
	// TimeoutMs is the mandatory await timeout (RULE 5).
	TimeoutMs int64 `json:"timeoutMs"`
	// AwaitIndex is the run's 1-based await ordinal: workflows/await
	// consumes a normal awaitIndex slot (RULE 3).
	AwaitIndex int `json:"awaitIndex"`
}

// workflowsAwaitChild is the awaited child's outcome on the response wire.
type workflowsAwaitChild struct {
	RunID      string `json:"runId"`
	Status     string `json:"status"`
	Summary    string `json:"summary,omitempty"`
	ErrorClass string `json:"errorClass,omitempty"`
}

// workflowsAwaitResponse is the events/await response shape plus the child
// outcome (present on terminal awaits; a timed-out await carries the child's
// current — possibly still non-terminal — state).
type workflowsAwaitResponse struct {
	awaitEventResponse
	Child *workflowsAwaitChild `json:"child,omitempty"`
}

// workflowsAwait is the workflows/await handler.
func (m *Module) workflowsAwait(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[workflowsAwaitParams](body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	childRunID := strings.TrimSpace(params.ChildRunID)
	if childRunID == "" {
		return nil, fmt.Errorf("childRunId required: %w", persistence.ErrInvalid)
	}
	child, err := m.execution.GetDriverRun(ctx, ws, childRunID)
	if err != nil {
		return nil, err
	}
	if child.ParentRunID == "" || child.ParentRunID != parent.RunID {
		return nil, fmt.Errorf("run %s is not a child of run %s: %w", childRunID, parent.RunID, persistence.ErrNotOwner)
	}
	outcome, err := m.awaitDriverRun(
		ctx, ws, id, parent, driverpkg.RunFinishedSubjectKey(childRunID),
		[]string{driverpkg.RunFinishedActor}, params.TimeoutMs, params.AwaitIndex,
	)
	if err != nil {
		return nil, err
	}
	resp := workflowsAwaitResponse{awaitEventResponse: m.executionAwaitEventResponse(ctx, ws, outcome)}
	if outcome.Status != execution.DriverAwaitOutcomeSuspended {
		if fresh, getErr := m.execution.GetDriverRun(ctx, ws, childRunID); getErr == nil {
			child = fresh
		}
		if outcome.Instance != nil && outcome.Instance.Status == execution.DriverAwaitSatisfied {
			if err := driverpkg.ValidateSatisfiedChildAwait(ctx, outcome.Instance, child); err != nil {
				return nil, err
			}
		}
		resp.Child = m.workflowsAwaitChild(ctx, ws, child)
	}
	return resp, nil
}

// workflowsAwaitChild renders the child outcome, re-reading the run so a
// satisfied await reports the terminal state recorded AFTER the await was
// registered (the child fetched at validation time may predate its finish).
func (m *Module) workflowsAwaitChild(ctx context.Context, ws string, child *execution.DriverRun) *workflowsAwaitChild {
	if child == nil {
		return nil
	}
	if fresh, err := m.execution.GetDriverRun(ctx, ws, child.RunID); err == nil {
		child = fresh
	}
	return &workflowsAwaitChild{
		RunID:      child.RunID,
		Status:     string(child.Status),
		Summary:    child.Summary,
		ErrorClass: child.ErrorClass,
	}
}

// handleWorkflowsStart serves POST /driver/workflows/start (two-segment path,
// explicitly registered like events/await).
func (m *Module) handleWorkflowsStart(w http.ResponseWriter, r *http.Request) {
	tokenID, ok := m.authenticate(w, r)
	if !ok {
		return
	}
	m.serveAuthorizedOp(w, r, m.workflowsStart, tokenID)
}

// handleWorkflowsAwait serves POST /driver/workflows/await.
func (m *Module) handleWorkflowsAwait(w http.ResponseWriter, r *http.Request) {
	tokenID, ok := m.authenticate(w, r)
	if !ok {
		return
	}
	m.serveAuthorizedOp(w, r, m.workflowsAwait, tokenID)
}
