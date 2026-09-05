package svcimpl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type failedSessionListStore struct{ store.Store }

func (s failedSessionListStore) AgentSessions() store.AgentSessionStore {
	return failedSessionList{AgentSessionStore: s.Store.AgentSessions()}
}

type failedSessionList struct{ store.AgentSessionStore }

func (s failedSessionList) List(context.Context, string, store.AgentSessionFilter) ([]*domain.AgentSession, error) {
	return nil, errors.New("session storage unavailable")
}

func TestTaskSessionRecoveryCannotAcknowledgeFailedControlPlaneRead(t *testing.T) {
	svc := NewSessionService(failedSessionListStore{Store: memstore.New()}, nil)
	items, err := svc.ListTaskSessions(t.Context(), "WS", "TASK-1")
	if err == nil {
		t.Fatalf("failed source acknowledged as sessions: %v", items)
	}
}

func TestTaskSessionRecoveryRejectsCorruptLocalIndex(t *testing.T) {
	runtimeDir := t.TempDir()
	local, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local.Dir(), "index.jsonl"), []byte("{broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := NewSessionServiceWithRuntimeDir(nil, nil, runtimeDir)
	if items, err := svc.ListTaskSessions(t.Context(), "WS", "TASK-1"); err == nil {
		t.Fatalf("corrupt local index acknowledged: %v", items)
	}
}

type failedSessionWorkspaceStore struct{ store.Store }

func (s failedSessionWorkspaceStore) Workspaces() store.WorkspaceStore {
	return failedSessionWorkspace{WorkspaceStore: s.Store.Workspaces()}
}

type failedSessionWorkspace struct{ store.WorkspaceStore }

func (s failedSessionWorkspace) Get(context.Context, string) (*domain.Workspace, error) {
	return nil, errors.New("workspace storage unavailable")
}

func TestTaskSessionRecoveryCannotHideWorkspaceReadFailureBehindRuntimeDir(t *testing.T) {
	svc := NewSessionServiceWithRuntimeDir(failedSessionWorkspaceStore{Store: memstore.New()}, nil, t.TempDir())
	if items, err := svc.ListTaskSessions(t.Context(), "WS", "TASK-1"); err == nil {
		t.Fatalf("failed workspace source acknowledged: %v", items)
	}
}
