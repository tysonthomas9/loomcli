package supervisor

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type releaseAsActorCall struct {
	id    string
	actor string
}

type releaseAsActorBackend struct {
	*clitest.MockIssueBackend
	calls []releaseAsActorCall
}

func (b *releaseAsActorBackend) ReleaseIssueAsActor(_ context.Context, id, actor string) error {
	b.calls = append(b.calls, releaseAsActorCall{id: id, actor: actor})
	return nil
}

func TestReleaseAssignedTaskClaim_FiresOnSessionComplete(t *testing.T) {
	backend := &releaseAsActorBackend{MockIssueBackend: clitest.NewMockIssueBackend()}
	s := newReleaseClaimTestSupervisor(t, backend)
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"}}

	s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
		sessionID: "session-1",
		exitCode:  0,
		taskID:    "task-1",
	})

	if len(backend.calls) != 1 {
		t.Fatalf("release calls = %#v, want one", backend.calls)
	}
	if got := backend.calls[0]; got.id != "task-1" || got.actor != "falcon" {
		t.Fatalf("release call = %#v, want task-1/falcon", got)
	}
}

func TestReleaseAssignedTaskClaim_NoopWhenBackendDoesNotSupportActorRelease(t *testing.T) {
	s := newReleaseClaimTestSupervisor(t, clitest.NewMockIssueBackend())
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"}}

	s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
		sessionID: "session-1",
		exitCode:  0,
		taskID:    "task-1",
	})
}

func TestReleaseAssignedTaskClaim_NoopOnEmptyTaskID(t *testing.T) {
	backend := &releaseAsActorBackend{MockIssueBackend: clitest.NewMockIssueBackend()}
	s := newReleaseClaimTestSupervisor(t, backend)
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"}}

	s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
		sessionID: "session-1",
		exitCode:  0,
	})

	if len(backend.calls) != 0 {
		t.Fatalf("release calls = %#v, want none for empty task id", backend.calls)
	}
}

func newReleaseClaimTestSupervisor(t *testing.T, ib backend.IssueBackend) *Supervisor {
	t.Helper()
	st := memstore.New()
	if _, err := st.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "session-1",
		AgentID:      "falcon",
		NodeID:       "node-1",
		Kind:         domain.AgentSessionKindTask,
		Status:       domain.AgentSessionStarting,
		Phase:        "implementation",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	s := newControlPlaneTestSupervisor(st)
	s.IssueBackend = ib
	return s
}
