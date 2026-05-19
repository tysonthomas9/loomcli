package supervisor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/sessionfinalize"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestControlPlaneNodeAndOwnershipLifecycle(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	t.Setenv("LOOM_FLEET_DB_ACTOR", "actor-1")

	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
	s.ControlStore = st
	s.WorkspaceID = "WS"
	s.NodeID = "node-1"
	s.NodeTTL = time.Minute
	s.NodeInterval = time.Hour
	s.Agents = []*AgentProcess{{Entry: cfgpkg.AgentEntry{Worktree: "worker"}}}
	if err := s.startControlPlaneNode(); err != nil {
		t.Fatalf("startControlPlaneNode: %v", err)
	}
	close(s.Shutdown)
	s.Wg.Wait()

	node, err := st.Nodes().Get(ctx, "WS", "node-1")
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if node.OwnerActor != "actor-1" || node.Capacity != 1 || node.DrainState != domain.NodeDrainActive {
		t.Fatalf("node = %+v", node)
	}

	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "worker", Role: "task"}}
	if !s.acquireAgentOwnership(ap) {
		t.Fatal("acquireAgentOwnership returned false")
	}
	if ap.OwnershipLeaseToken == "" || ap.OwnershipFencingToken == 0 {
		t.Fatalf("ownership lease was not copied to agent: %+v", ap)
	}
	if !s.heartbeatAgentOwnership(ap, time.Minute) {
		t.Fatal("heartbeatAgentOwnership returned false")
	}
	s.releaseAgentOwnership(ap)
	if ap.OwnershipLeaseToken != "" || ap.OwnershipFencingToken != 0 || !ap.OwnershipLastHeartbeat.IsZero() {
		t.Fatalf("ownership state was not cleared: token=%q fencing=%d heartbeat=%v", ap.OwnershipLeaseToken, ap.OwnershipFencingToken, ap.OwnershipLastHeartbeat)
	}
}

func TestOwnershipConflictAndInterruptibleSleeps(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.AgentOwnershipLeases().Acquire(ctx, store.AgentOwnershipLeaseAcquire{
		WorkspaceKey:    "WS",
		AgentID:         "worker",
		OwnerID:         "other-node",
		RuntimeProvider: domain.RuntimeProviderLocal,
		NodeID:          "other-node",
		TTL:             time.Minute,
	}); err != nil {
		t.Fatalf("seed ownership lease: %v", err)
	}

	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
	s.ControlStore = st
	s.WorkspaceID = "WS"
	s.NodeID = "node-1"
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "worker", Role: "task"}, StopCh: make(chan struct{})}
	if s.acquireAgentOwnership(ap) {
		t.Fatal("acquireAgentOwnership returned true for a conflicting lease")
	}

	close(s.Shutdown)
	if s.sleepBeforeOwnershipRetry(ap) {
		t.Fatal("sleepBeforeOwnershipRetry returned true after shutdown")
	}
	if ap.StopReason != StopReasonShutdown || !ap.BackoffUntil.IsZero() {
		t.Fatalf("shutdown retry state = reason %q backoff %v", ap.StopReason, ap.BackoffUntil)
	}

	s = newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
	s.EmitEvent = func(events.Event) {}
	ap = &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "worker", Role: "task"}, StopCh: make(chan struct{})}
	close(ap.StopCh)
	if s.sleepBeforeRestart(ap) {
		t.Fatal("sleepBeforeRestart returned true after stop signal")
	}
	if ap.StopReason != StopReasonConfigRemoved || !ap.BackoffUntil.IsZero() {
		t.Fatalf("restart stop state = reason %q backoff %v", ap.StopReason, ap.BackoffUntil)
	}
}

func TestControlPlaneAgentSessionLifecycleAndMetadata(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
	s.ControlStore = st
	s.WorkspaceID = "WS"
	s.NodeID = "node-1"

	ap := &AgentProcess{
		Entry: cfgpkg.AgentEntry{
			Worktree: "worker",
			Role:     "task",
			Backend:  "codex",
			Mode:     domain.AgentModeEphemeral,
			Repo:     "api",
		},
		AssignedTaskID:  "TASK-1",
		AgentSessionID:  "sess-1",
		ParentSessionID: "orch-1",
		TranscriptPath:  "/tmp/transcript.jsonl",
		LogFilePath:     "/tmp/worker.log",
	}
	s.createControlPlaneAgentSession(ap, "sess-1", "EPIC-1", "implementation", 2)
	session, err := st.AgentSessions().Get(ctx, "WS", "sess-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Status != domain.AgentSessionStarting || session.ParentSessionID != "orch-1" || session.Attempt != 2 {
		t.Fatalf("created session = %+v", session)
	}
	for key, want := range map[string]string{
		"backend":         "codex",
		"epic_id":         "EPIC-1",
		"task_id":         "TASK-1",
		"attempt_kind":    "ephemeral_task_attempt",
		"cleanup_state":   "retained",
		"repo":            "api",
		"transcript_path": "/tmp/transcript.jsonl",
		"log_path":        "/tmp/worker.log",
	} {
		if session.Metadata[key] != want {
			t.Fatalf("metadata[%s] = %q, want %q in %#v", key, session.Metadata[key], want, session.Metadata)
		}
	}
	if ap.AgentLeaseID == "" || ap.AgentLeaseToken == "" {
		t.Fatalf("agent lease was not stored on process: lease=%q token=%q", ap.AgentLeaseID, ap.AgentLeaseToken)
	}

	s.markControlPlaneAgentSessionRunning(ap)
	session, err = st.AgentSessions().Get(ctx, "WS", "sess-1")
	if err != nil {
		t.Fatalf("get running session: %v", err)
	}
	if session.Status != domain.AgentSessionRunning || session.LastHeartbeat.IsZero() {
		t.Fatalf("running session = %+v", session)
	}

	s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
		sessionID:  "sess-1",
		leaseID:    ap.AgentLeaseID,
		leaseToken: ap.AgentLeaseToken,
		exitCode:   1,
		errClass:   agenterr.NoWork.String(),
		taskID:     "TASK-2",
		diffResult: sessionfinalize.WithWorktreeResult{
			DiffStats: sessions.DiffStats{FilesChanged: 2, LinesAdded: 3, LinesRemoved: 4},
			FilesTouched: []string{
				"a.go",
				"b.go",
			},
			HasDiffPatch: true,
		},
	})
	session, err = st.AgentSessions().Get(ctx, "WS", "sess-1")
	if err != nil {
		t.Fatalf("get completed session: %v", err)
	}
	if session.Status != domain.AgentSessionFailed || session.TaskID != "TASK-2" || session.ErrorClass != agenterr.NoWork.String() {
		t.Fatalf("completed session = %+v", session)
	}
	if session.ExitCode == nil || *session.ExitCode != 1 || session.FinishedAt == nil {
		t.Fatalf("completion fields missing: %+v", session)
	}
	if !strings.Contains(session.Metadata["files_touched"], "a.go") || session.Metadata["diff_path"] != "diff.patch" {
		t.Fatalf("diff metadata missing: %#v", session.Metadata)
	}
}

func TestSessionFinalizeHelpers(t *testing.T) {
	ap := &AgentProcess{
		AgentSessionID:  "sess-1",
		AgentLeaseID:    "lease-1",
		AgentLeaseToken: "token-1",
		BeforeRef:       "before",
		LastError:       &agenterr.AgentError{Class: agenterr.RateLimited},
	}
	state := takeAgentSessionForFinalize(ap)
	if state.sessionID != "sess-1" || state.leaseID != "lease-1" || state.leaseToken != "token-1" || state.beforeRef != "before" {
		t.Fatalf("state = %+v", state)
	}
	if ap.AgentSessionID != "" || ap.AgentLeaseID != "" || ap.AgentLeaseToken != "" {
		t.Fatalf("agent session state was not cleared: %+v", ap)
	}
	if got := agentErrorClass(ap); got != agenterr.RateLimited.String() {
		t.Fatalf("agentErrorClass = %q", got)
	}
	if result := finalizeLocalSession(nil, &AgentProcess{WorktreePath: t.TempDir()}, "", "TASK-1", 0, ""); result.HasDiffPatch {
		t.Fatalf("nil local session should not report diff patch: %+v", result)
	}
}
