package handler

import (
	"encoding/json"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
)

// DriverRunsFromDomain maps the legacy read projection explicitly into the
// canonical generated HTTP contract. WebUI readers share this mapping until
// the legacy read model is retired.
func DriverRunsFromDomain(runs []*domain.DriverRun) []loomapi.DriverRun {
	out := make([]loomapi.DriverRun, 0, len(runs))
	for _, run := range runs {
		if run != nil {
			out = append(out, DriverRunFromDomain(run))
		}
	}
	return out
}

// DriverRunFromDomain maps one legacy DriverRun into the generated schema.
func DriverRunFromDomain(run *domain.DriverRun) loomapi.DriverRun {
	var payload interface{}
	if len(run.Payload) != 0 {
		payload = json.RawMessage(append([]byte(nil), run.Payload...))
	}
	return loomapi.DriverRun{
		WorkspaceKey: run.WorkspaceKey, RunId: run.RunID, DriverId: run.DriverID, DriverVersionId: run.DriverVersionID,
		Entrypoint: optionalContractString(run.Entrypoint), SourceKind: optionalContractString(run.SourceKind),
		SourceRef: optionalContractString(run.SourceRef), EpicId: optionalContractString(run.EpicID),
		TriggerBindingId: optionalContractString(run.TriggerBindingID), AgentServiceId: optionalContractString(run.AgentServiceID),
		SubjectKey: optionalContractString(run.SubjectKey), Status: string(run.Status),
		NodeId: optionalContractString(run.NodeID), LeaseId: optionalContractString(run.LeaseID),
		FencingToken: optionalContractInt64(run.FencingToken), IdempotencyKey: optionalContractString(run.IdempotencyKey),
		Payload: payload, Output: optionalContractMap(run.Output), Summary: optionalContractString(run.Summary),
		ErrorClass: optionalContractString(run.ErrorClass), StartedAt: optionalContractTime(run.StartedAt),
		LastHeartbeat: optionalContractTime(run.LastHeartbeat), FinishedAt: run.FinishedAt,
		ParentRunId: optionalContractString(run.ParentRunID), AwaitInstanceKey: optionalContractString(run.AwaitInstanceKey),
		SuspendedAt: run.SuspendedAt, CancelRequestedAt: run.CancelRequestedAt,
		CancelRequestedReason: optionalContractString(run.CancelRequestedReason),
		ResumeSourceEventId:   optionalContractString(run.ResumeSourceEventID), CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
	}
}

func optionalContractString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalContractInt64(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}

func optionalContractTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func optionalContractMap(value map[string]string) *map[string]string {
	if value == nil {
		return nil
	}
	cloned := make(map[string]string, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return &cloned
}
