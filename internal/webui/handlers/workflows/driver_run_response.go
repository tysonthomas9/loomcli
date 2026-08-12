package workflows

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
)

type driverRunResponse = loomapi.DriverRun

func newDriverRunResponse(run *execution.DriverRun) (*driverRunResponse, error) {
	if run == nil {
		return nil, fmt.Errorf("execution returned no DriverRun: %w", execution.ErrConflict)
	}
	var payload interface{}
	if len(run.Payload) != 0 {
		payload = json.RawMessage(append([]byte(nil), run.Payload...))
	}
	return &loomapi.DriverRun{
		WorkspaceKey: run.WorkspaceKey, RunId: run.RunID, DriverId: run.DriverID, DriverVersionId: run.DriverVersionID,
		Entrypoint: optionalDriverRunString(run.Entrypoint), SourceKind: optionalDriverRunString(run.SourceKind),
		SourceRef: optionalDriverRunString(run.SourceRef), EpicId: optionalDriverRunString(run.EpicID),
		TriggerBindingId: optionalDriverRunString(run.TriggerBindingID), AgentServiceId: optionalDriverRunString(run.AgentServiceID),
		SubjectKey: optionalDriverRunString(run.SubjectKey), Status: string(run.Status),
		NodeId: optionalDriverRunString(run.Owner.NodeID), LeaseId: optionalDriverRunString(run.Owner.LeaseID),
		FencingToken: optionalDriverRunInt64(run.Owner.FencingToken), IdempotencyKey: optionalDriverRunString(run.IdempotencyKey),
		Payload: payload, Output: optionalDriverRunMap(run.Output), Summary: optionalDriverRunString(run.Summary),
		ErrorClass: optionalDriverRunString(run.ErrorClass), StartedAt: optionalDriverRunTime(run.StartedAt),
		LastHeartbeat: optionalDriverRunTime(run.LastHeartbeat), FinishedAt: run.FinishedAt,
		ParentRunId: optionalDriverRunString(run.ParentRunID), AwaitInstanceKey: optionalDriverRunString(run.AwaitInstanceKey),
		SuspendedAt: run.SuspendedAt, CancelRequestedAt: run.CancelRequestedAt,
		CancelRequestedReason: optionalDriverRunString(run.CancelRequestedReason),
		ResumeSourceEventId:   optionalDriverRunString(run.ResumeSourceEventID), CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
	}, nil
}

func optionalDriverRunString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalDriverRunInt64(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}

func optionalDriverRunTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func optionalDriverRunMap(value map[string]string) *map[string]string {
	cloned := cloneDriverRunResponseMap(value)
	if cloned == nil {
		return nil
	}
	return &cloned
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
