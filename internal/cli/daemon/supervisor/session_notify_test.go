package supervisor

import (
	"context"
	"errors"
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type observedSessionChange struct {
	workspaceID string
	taskID      string
	sessionID   string
	status      sessions.SessionStatus
}

type failingAgentSessionUpdateStore struct {
	store.AgentSessionStore
}

func (s *failingAgentSessionUpdateStore) Update(context.Context, string, string, store.AgentSessionUpdate) (*domain.AgentSession, error) {
	return nil, errors.New("injected update failure")
}

func seedRunningAgentSession(t *testing.T, st *memstore.Store, sessionID string) {
	t.Helper()
	if _, err := st.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    sessionID,
		AgentID:      "worker-1",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "task-before",
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("seed agent session: %v", err)
	}
}

func TestCompleteControlPlaneAgentSessionPublishesAuthoritativeSessionChange(t *testing.T) {
	st := memstore.New()
	seedRunningAgentSession(t, st, "session-1")
	notified := make(chan observedSessionChange, 1)
	s := newControlPlaneTestSupervisor(st)
	s.notifySessionChange = func(_ context.Context, workspaceID, taskID, sessionID string, status sessions.SessionStatus) {
		notified <- observedSessionChange{workspaceID: workspaceID, taskID: taskID, sessionID: sessionID, status: status}
	}

	s.completeControlPlaneAgentSession(&AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "worker-1"}}, agentSessionCompletionInput{
		sessionID: "session-1",
		taskID:    "task-final",
		exitCode:  0,
	})

	select {
	case got := <-notified:
		want := observedSessionChange{workspaceID: "WS", taskID: "task-final", sessionID: "session-1", status: sessions.StatusCompleted}
		if got != want {
			t.Fatalf("session change = %+v, want %+v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("successful authoritative session update emitted no UI notification")
	}
}

func TestCompleteControlPlaneAgentSessionSuppressesNotificationWhenUpdateFails(t *testing.T) {
	st := memstore.New()
	seedRunningAgentSession(t, st, "session-1")
	notified := make(chan observedSessionChange, 1)
	s := newControlPlaneTestSupervisor(st)
	s.ControlStore = &controlPlaneStoreOverrides{
		Store: st,
		sessions: &failingAgentSessionUpdateStore{
			AgentSessionStore: st.AgentSessions(),
		},
	}
	s.notifySessionChange = func(_ context.Context, workspaceID, taskID, sessionID string, status sessions.SessionStatus) {
		notified <- observedSessionChange{workspaceID: workspaceID, taskID: taskID, sessionID: sessionID, status: status}
	}

	s.completeControlPlaneAgentSession(&AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "worker-1"}}, agentSessionCompletionInput{
		sessionID: "session-1",
		taskID:    "task-final",
		exitCode:  0,
	})

	select {
	case got := <-notified:
		t.Fatalf("failed authoritative update emitted notification: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
}
