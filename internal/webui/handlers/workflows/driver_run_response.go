package workflows

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

// driverRunResponse is the Workflows transport projection of Execution's
// public snapshot. Keeping the wire shape here avoids routing current
// Execution state back through the retired horizontal domain model.
type driverRunResponse struct {
	WorkspaceKey          string                    `json:"workspace_key"`
	RunID                 string                    `json:"run_id"`
	DriverID              string                    `json:"driver_id"`
	DriverVersionID       string                    `json:"driver_version_id"`
	Entrypoint            string                    `json:"entrypoint,omitempty"`
	SourceKind            string                    `json:"source_kind,omitempty"`
	SourceRef             string                    `json:"source_ref,omitempty"`
	EpicID                string                    `json:"epic_id,omitempty"`
	TriggerBindingID      string                    `json:"trigger_binding_id,omitempty"`
	AgentServiceID        string                    `json:"agent_service_id,omitempty"`
	SubjectKey            string                    `json:"subject_key,omitempty"`
	Status                execution.DriverRunStatus `json:"status"`
	NodeID                string                    `json:"node_id,omitempty"`
	LeaseID               string                    `json:"lease_id,omitempty"`
	FencingToken          int64                     `json:"fencing_token,omitempty"`
	IdempotencyKey        string                    `json:"idempotency_key,omitempty"`
	Payload               json.RawMessage           `json:"payload,omitempty"`
	Output                map[string]string         `json:"output,omitempty"`
	Summary               string                    `json:"summary,omitempty"`
	ErrorClass            string                    `json:"error_class,omitempty"`
	StartedAt             time.Time                 `json:"started_at,omitempty"`
	LastHeartbeat         time.Time                 `json:"last_heartbeat,omitempty"`
	FinishedAt            *time.Time                `json:"finished_at,omitempty"`
	ParentRunID           string                    `json:"parent_run_id,omitempty"`
	AwaitInstanceKey      string                    `json:"await_instance_key,omitempty"`
	SuspendedAt           *time.Time                `json:"suspended_at,omitempty"`
	CancelRequestedAt     *time.Time                `json:"cancel_requested_at,omitempty"`
	CancelRequestedReason string                    `json:"cancel_requested_reason,omitempty"`
	ResumeSourceEventID   string                    `json:"resume_source_event_id,omitempty"`
	CreatedAt             time.Time                 `json:"created_at"`
	UpdatedAt             time.Time                 `json:"updated_at"`
}

func newDriverRunResponse(run *execution.DriverRun) (*driverRunResponse, error) {
	if run == nil {
		return nil, fmt.Errorf("Execution returned no DriverRun: %w", execution.ErrConflict)
	}
	return &driverRunResponse{
		WorkspaceKey: run.WorkspaceKey, RunID: run.RunID, DriverID: run.DriverID, DriverVersionID: run.DriverVersionID,
		Entrypoint: run.Entrypoint, SourceKind: run.SourceKind, SourceRef: run.SourceRef, EpicID: run.EpicID,
		TriggerBindingID: run.TriggerBindingID, AgentServiceID: run.AgentServiceID, SubjectKey: run.SubjectKey,
		Status: run.Status, NodeID: run.Owner.NodeID, LeaseID: run.Owner.LeaseID, FencingToken: run.Owner.FencingToken,
		IdempotencyKey: run.IdempotencyKey, Payload: append(json.RawMessage(nil), run.Payload...),
		Output: cloneDriverRunResponseMap(run.Output), Summary: run.Summary, ErrorClass: run.ErrorClass,
		StartedAt: run.StartedAt, LastHeartbeat: run.LastHeartbeat, FinishedAt: run.FinishedAt,
		ParentRunID: run.ParentRunID, AwaitInstanceKey: run.AwaitInstanceKey, SuspendedAt: run.SuspendedAt,
		CancelRequestedAt: run.CancelRequestedAt, CancelRequestedReason: run.CancelRequestedReason,
		ResumeSourceEventID: run.ResumeSourceEventID, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
	}, nil
}

func cloneDriverRunResponseMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func driverRunResponses(runs []*execution.DriverRun, limit int) ([]*driverRunResponse, error) {
	sort.SliceStable(runs, func(i, j int) bool {
		return runs[i].StartedAt.After(runs[j].StartedAt)
	})
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	responses := make([]*driverRunResponse, 0, len(runs))
	for _, run := range runs {
		response, err := newDriverRunResponse(run)
		if err != nil {
			return nil, err
		}
		responses = append(responses, response)
	}
	return responses, nil
}
