package driver

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func seedFlueSession(t *testing.T, st *memstore.Store) *flueTaskSession {
	t.Helper()
	meta := map[string]string{"runtime": "flue", "task_run_id": "task-run-1"}
	if _, err := st.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
		WorkspaceKey: "TEST",
		SessionID:    "flue-task-run-1",
		AgentID:      "flue-task-agent",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TEST-1",
		Status:       domain.AgentSessionRunning,
		Metadata:     meta,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return &flueTaskSession{SessionID: "flue-task-run-1", Metadata: meta}
}

// The driver parses token/cost usage out of the runner result (taskExecResult)
// and forwards it to the platform TaskRun, but the control-plane AgentSession —
// the record the Runs tab reads — was closed out without it. The WebUI could
// therefore never show telemetry the driver already held.
func TestFinishFlueTaskSessionPersistsUsage(t *testing.T) {
	st := memstore.New()
	session := seedFlueSession(t, st)
	exec := HostBridgeTaskExecutor{Store: st}

	result := TaskExecResult{
		Status:           domain.TaskRunCompleted,
		ExitCode:         0,
		InputTokens:      1200,
		OutputTokens:     340,
		CacheReadTokens:  56,
		CacheWriteTokens: 7,
		EstimatedCostUSD: 0.0125,
	}
	if err := exec.finishFlueTaskSession(t.Context(), TaskExecRequest{WorkspaceKey: "TEST"}, session, result, nil, nil); err != nil {
		t.Fatalf("finishFlueTaskSession: %v", err)
	}

	rec, err := st.AgentSessions().Get(t.Context(), "TEST", "flue-task-run-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	usage, ok := sessions.DecodeUsageMetadata(rec.Metadata)
	if !ok {
		t.Fatalf("no usage recorded on session metadata: %+v", rec.Metadata)
	}
	if usage.InputTokens != 1200 || usage.OutputTokens != 340 {
		t.Fatalf("tokens = %d/%d, want 1200/340", usage.InputTokens, usage.OutputTokens)
	}
	if usage.CacheReadTokens != 56 || usage.CacheWriteTokens != 7 {
		t.Fatalf("cache tokens = %d/%d, want 56/7", usage.CacheReadTokens, usage.CacheWriteTokens)
	}
	if usage.EstimatedCostUSD != 0.0125 {
		t.Fatalf("cost = %v, want 0.0125", usage.EstimatedCostUSD)
	}
}

// A backend that reports no usage at all must not be recorded as a real
// zero-token run — absence has to stay distinguishable.
func TestFinishFlueTaskSessionOmitsUsageWhenNoneReported(t *testing.T) {
	st := memstore.New()
	session := seedFlueSession(t, st)
	exec := HostBridgeTaskExecutor{Store: st}

	result := TaskExecResult{Status: domain.TaskRunCompleted, ExitCode: 0}
	if err := exec.finishFlueTaskSession(t.Context(), TaskExecRequest{WorkspaceKey: "TEST"}, session, result, nil, nil); err != nil {
		t.Fatalf("finishFlueTaskSession: %v", err)
	}

	rec, err := st.AgentSessions().Get(t.Context(), "TEST", "flue-task-run-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if _, ok := sessions.DecodeUsageMetadata(rec.Metadata); ok {
		t.Fatalf("usage recorded for a run that reported none: %+v", rec.Metadata)
	}
}
