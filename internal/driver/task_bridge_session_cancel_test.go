package driver

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type cancelRejectingSessions struct{ store.AgentSessionStore }

func (s cancelRejectingSessions) Update(ctx context.Context, workspaceKey, sessionID string, patch store.AgentSessionUpdate) (*domain.AgentSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.AgentSessionStore.Update(ctx, workspaceKey, sessionID, patch)
}

type cancelRejectingStore struct{ store.Store }

func (s cancelRejectingStore) AgentSessions() store.AgentSessionStore {
	return cancelRejectingSessions{s.Store.AgentSessions()}
}

func TestFinishFlueTaskSessionAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	st := cancelRejectingStore{memstore.New()}
	req := hostBridgeTaskExecRequest()
	req.RunnerKind = RunnerKindFlueWorkflow
	executor := HostBridgeTaskExecutor{Store: st}
	session, err := executor.startFlueTaskSession(ctx, req)
	if err != nil || session == nil {
		t.Fatalf("start session = %+v, %v", session, err)
	}
	cancel()
	result := TaskExecResult{Status: domain.TaskRunCancelled, ExitCode: 130}
	if err := executor.finishFlueTaskSession(ctx, req, session, result, nil, nil); err != nil {
		t.Fatalf("finish cancelled session: %v", err)
	}
	stored, err := st.AgentSessions().Get(context.Background(), req.WorkspaceKey, session.SessionID)
	if err != nil || stored.Status != domain.AgentSessionCancelled || stored.FinishedAt == nil {
		t.Fatalf("session = %+v, %v; want finalized cancellation", stored, err)
	}
}
