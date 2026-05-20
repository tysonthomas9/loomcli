package supervisor

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/sessionfinalize"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestControlPlaneAgentSessionLifecycleBranches(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	s := newTestSupervisorWithConfig(&config.DaemonConfig{Backend: "codex"})
	s.ControlStore = st
	s.WorkspaceID = "WS"
	s.NodeID = "node-1"
	ap := &AgentProcess{
		Entry: config.AgentEntry{
			Worktree: "nova",
			Role:     "task",
			Repo:     "api",
			Mode:     domain.AgentModeEphemeral,
			Backend:  "codex",
		},
		AssignedTaskID:  "TASK-1",
		AgentSessionID:  "session-1",
		TranscriptPath:  "transcript.jsonl",
		LogFilePath:     "agent.log",
		ParentSessionID: "lead-session",
	}

	s.createControlPlaneAgentSession(ap, "session-1", "EPIC-1", "implementation", 3)
	if ap.AgentLeaseID == "" || ap.AgentLeaseToken == "" {
		t.Fatalf("lease state was not recorded: lease=%q token=%q", ap.AgentLeaseID, ap.AgentLeaseToken)
	}
	session, err := st.AgentSessions().Get(ctx, "WS", "session-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Status != domain.AgentSessionStarting || session.NodeID != "node-1" || session.Metadata["cleanup_state"] != "retained" {
		t.Fatalf("created session = %+v", session)
	}

	ap.AgentSessionID = "session-1"
	s.markControlPlaneAgentSessionRunning(ap)
	session, err = st.AgentSessions().Get(ctx, "WS", "session-1")
	if err != nil {
		t.Fatalf("get running session: %v", err)
	}
	if session.Status != domain.AgentSessionRunning || session.LastHeartbeat.IsZero() {
		t.Fatalf("running session = %+v", session)
	}

	s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
		sessionID:  "session-1",
		leaseID:    ap.AgentLeaseID,
		leaseToken: ap.AgentLeaseToken,
		exitCode:   7,
		errClass:   "tool_error",
		taskID:     "TASK-2",
		diffResult: sessionfinalize.WithWorktreeResult{
			DiffStats:    sessions.DiffStats{FilesChanged: 2, LinesAdded: 3, LinesRemoved: 1},
			FilesTouched: []string{"a.go", "b.go"},
			HasDiffPatch: true,
		},
	})
	session, err = st.AgentSessions().Get(ctx, "WS", "session-1")
	if err != nil {
		t.Fatalf("get completed session: %v", err)
	}
	if session.Status != domain.AgentSessionFailed || session.TaskID != "TASK-2" ||
		session.ErrorClass != "tool_error" || session.Metadata["files_changed"] != "2" {
		t.Fatalf("completed session = %+v", session)
	}
	lease, err := st.AgentLeases().Get(ctx, "WS", ap.AgentLeaseID)
	if err != nil {
		t.Fatalf("get released lease: %v", err)
	}
	if lease.Status != domain.AgentLeaseReleased {
		t.Fatalf("lease = %+v", lease)
	}

	state := takeAgentSessionForFinalize(&AgentProcess{
		Session:         nil,
		AgentSessionID:  "session-2",
		AgentLeaseID:    "lease-2",
		AgentLeaseToken: "token-2",
		BeforeRef:       "abc123",
	})
	if state.sessionID != "session-2" || state.beforeRef != "abc123" {
		t.Fatalf("finalize state = %+v", state)
	}
}
