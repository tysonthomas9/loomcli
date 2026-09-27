//go:build sessionsreplay

package driver

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// #238: the already-exists path must preserve the first terminal status.
func TestSessionsReplay238ReopenTerminal(t *testing.T) {
	store := memstore.New()
	req := TaskExecRequest{
		WorkspaceKey: "WS", TaskRunID: "run-1", TaskID: "TASK-1",
		RunnerKind: RunnerKindFlueWorkflow,
	}
	executor := HostBridgeTaskExecutor{Store: store}
	session, err := executor.startFlueTaskSession(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.finishFlueTaskSession(t.Context(), req, session,
		TaskExecResult{Status: domain.TaskRunCompleted}, nil, nil); err != nil {
		t.Fatal(err)
	}
	_, _ = executor.startFlueTaskSession(t.Context(), req)
	record, err := store.AgentSessions().Get(t.Context(), "WS", session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != domain.AgentSessionCompleted {
		t.Fatalf("#238: re-open changed terminal session to %q", record.Status)
	}
}

// #375: runner-reported usage must survive the driver session close.
func TestSessionsReplay375UsageOnClose(t *testing.T) {
	st := memstore.New()
	metadata := map[string]string{"runtime": "flue", "task_run_id": "run-2"}
	_, err := st.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "flue-run-2", AgentID: "worker",
		Status: domain.AgentSessionRunning,
		Metadata: metadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := HostBridgeTaskExecutor{Store: st}
	err = executor.finishFlueTaskSession(t.Context(), TaskExecRequest{WorkspaceKey: "WS"},
		&flueTaskSession{SessionID: "flue-run-2", Metadata: metadata},
		TaskExecResult{Status: domain.TaskRunCompleted, InputTokens: 1200, OutputTokens: 340}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	record, err := st.AgentSessions().Get(t.Context(), "WS", "flue-run-2")
	if err != nil {
		t.Fatal(err)
	}
	if record.Metadata["input_tokens"] != "1200" || record.Metadata["output_tokens"] != "340" {
		t.Fatalf("#375: runner usage missing from session metadata: %+v", record.Metadata)
	}
}
