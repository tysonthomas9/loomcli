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
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
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

// workflowsStart is the workflows/start handler.
func (m *Module) workflowsStart(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[workflowsStartParams](body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	child, err := driverpkg.StartChildWorkflow(ctx, m.store, driverpkg.StartChildWorkflowOptions{
		WorkspaceKey:   ws,
		ParentRunID:    parent.RunID,
		WorkflowName:   strings.TrimSpace(params.WorkflowName),
		Input:          params.Input,
		IdempotencyKey: params.IdempotencyKey,
		StartIndex:     params.StartIndex,
	})
	if err != nil {
		return nil, err
	}
	return workflowsStartResponse{
		ChildRunID:   child.RunID,
		WorkflowName: child.DriverID,
		Status:       string(child.Status),
		ParentRunID:  child.ParentRunID,
	}, nil
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
	fence, err := id.FencingToken()
	if err != nil {
		return nil, err
	}
	outcome, child, err := driverpkg.AwaitChildWorkflow(ctx, m.store, driverpkg.AwaitChildWorkflowOptions{
		WorkspaceKey: ws,
		RunID:        parent.RunID,
		NodeID:       id.NodeID,
		LeaseID:      id.LeaseID,
		FencingToken: fence,
		ChildRunID:   params.ChildRunID,
		TimeoutMs:    params.TimeoutMs,
		AwaitIndex:   params.AwaitIndex,
	})
	if err != nil {
		return nil, err
	}
	resp := workflowsAwaitResponse{awaitEventResponse: m.awaitEventResponse(ctx, ws, outcome)}
	if outcome.Status != driverpkg.AwaitOutcomeSuspended {
		resp.Child = m.workflowsAwaitChild(ctx, ws, child)
	}
	return resp, nil
}

// workflowsAwaitChild renders the child outcome, re-reading the run so a
// satisfied await reports the terminal state recorded AFTER the await was
// registered (the child fetched at validation time may predate its finish).
func (m *Module) workflowsAwaitChild(ctx context.Context, ws string, child *domain.DriverRun) *workflowsAwaitChild {
	if child == nil {
		return nil
	}
	if fresh, err := m.store.DriverRuns().Get(ctx, ws, child.RunID); err == nil {
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
	if !m.authorize(w, r) {
		return
	}
	m.serveAuthorizedOp(w, r, m.workflowsStart)
}

// handleWorkflowsAwait serves POST /driver/workflows/await.
func (m *Module) handleWorkflowsAwait(w http.ResponseWriter, r *http.Request) {
	if !m.authorize(w, r) {
		return
	}
	m.serveAuthorizedOp(w, r, m.workflowsAwait)
}
