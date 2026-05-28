package supervisor

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func isolateSupervisorWorkspaceRuntimeDir(t *testing.T) string {
	t.Helper()
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)
	return runtimeDir
}

func stubEmptyProcessInspector(t *testing.T) {
	t.Helper()
	old := procInspector
	procInspector = processInspector{
		List: func() ([]procInfo, error) { return nil, nil },
		CWD:  func(int) (string, error) { return "", nil },
		CWDs: func([]int) (map[int]string, error) { return nil, nil },
	}
	t.Cleanup(func() { procInspector = old })
}

func TestStartControlPlaneNodeDefaultsAndNoopBranches(t *testing.T) {
	if err := (&Supervisor{}).startControlPlaneNode(); err != nil {
		t.Fatalf("empty startControlPlaneNode: %v", err)
	}
	if got := resolveNodeOwnerActor(); got == "" {
		t.Fatal("resolveNodeOwnerActor returned empty actor")
	}

	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
	s.ControlStore = st
	s.WorkspaceID = "WS"
	s.NodeInterval = time.Hour
	s.Agents = []*AgentProcess{
		{Entry: cfgpkg.AgentEntry{Worktree: "one"}},
		{Entry: cfgpkg.AgentEntry{Worktree: "two"}},
	}
	if err := s.startControlPlaneNode(); err != nil {
		t.Fatalf("startControlPlaneNode defaults: %v", err)
	}
	close(s.Shutdown)
	s.Wg.Wait()
	if s.NodeID == "" {
		t.Fatal("startControlPlaneNode did not assign NodeID")
	}
	node, err := st.Nodes().Get(ctx, "WS", s.NodeID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if node.Capacity != 2 || node.RuntimeProvider != domain.RuntimeProviderLocal {
		t.Fatalf("node = %+v, want default capacity/runtime", node)
	}
}

func TestSupervisorLifecycleHelpersAdditionalBranches(t *testing.T) {
	zero := 0
	cfg := &cfgpkg.DaemonConfig{
		Backend: "fleet",
		Daemon: cfgpkg.DaemonSettings{RestartPolicy: cfgpkg.RestartPolicy{
			BackoffInitial: &zero,
			BackoffMax:     &zero,
		}},
	}

	t.Run("start fleet mode skips agent supervision", func(t *testing.T) {
		s := newTestSupervisorWithConfig(cfg)
		s.Concurrency = NewConcurrencyTracker(nil)
		s.EmitEvent = func(events.Event) {}
		if err := s.Start(); err != nil {
			t.Fatalf("Start fleet mode: %v", err)
		}
		close(s.Shutdown)
		s.Wg.Wait()
	})

	t.Run("start non-fleet mode with no agents", func(t *testing.T) {
		s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
		s.Concurrency = NewConcurrencyTracker(nil)
		s.EmitEvent = func(events.Event) {}
		if err := s.Start(); err != nil {
			t.Fatalf("Start non-fleet mode: %v", err)
		}
		s.Stop()
	})

	t.Run("start non-fleet mode launches configured agent goroutine", func(t *testing.T) {
		stubEmptyProcessInspector(t)
		s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
		s.Concurrency = NewConcurrencyTracker(nil)
		s.Concurrency.Close()
		s.EmitEvent = func(events.Event) {}
		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "nova", Role: "task"},
			WorktreePath: t.TempDir(),
		}
		s.Agents = []*AgentProcess{ap}
		if err := s.Start(); err != nil {
			t.Fatalf("Start non-fleet with agent: %v", err)
		}
		select {
		case <-ap.Done:
		case <-time.After(time.Second):
			t.Fatal("agent supervisor goroutine did not exit")
		}
		if ap.StopReason != StopReasonShutdown {
			t.Fatalf("StopReason = %q, want shutdown", ap.StopReason)
		}
		s.Stop()
	})

	t.Run("supervise exits when concurrency tracker is closed", func(t *testing.T) {
		s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
		s.Concurrency = NewConcurrencyTracker(nil)
		s.Concurrency.Close()
		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "nova", Role: "task"},
			WorktreePath: t.TempDir(),
			StopCh:       make(chan struct{}),
		}
		s.superviseAgent(ap)
		if ap.StopReason != StopReasonShutdown {
			t.Fatalf("StopReason = %q, want shutdown", ap.StopReason)
		}
	})

	t.Run("preflight records no work", func(t *testing.T) {
		s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
		s.EmitEvent = func(events.Event) {}
		s.IssueBackend = &supervisorNoReadyBackend{}
		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "nova", Role: "task"},
			WorktreePath: t.TempDir(),
			StopCh:       make(chan struct{}),
		}
		if s.preFlightSetup(ap) {
			t.Fatal("preFlightSetup succeeded despite no ready work")
		}
		if ap.LastError == nil || ap.LastError.Class != agenterr.NoWork {
			t.Fatalf("LastError = %+v, want NoWork preflight error", ap.LastError)
		}
	})

	t.Run("supervise observes shutdown after spawn failure iteration", func(t *testing.T) {
		isolateSupervisorWorkspaceRuntimeDir(t)
		one := 1
		s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{RestartPolicy: cfgpkg.RestartPolicy{
			MaxRetries:     &one,
			BackoffInitial: &one,
			BackoffMax:     &one,
		}}})
		s.Concurrency = NewConcurrencyTracker(nil)
		s.EmitEvent = func(events.Event) {}
		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "custom-worker", Role: "custom"},
			RoleConfig:   cfgpkg.RoleConfig{},
			WorktreePath: t.TempDir(),
			StopCh:       make(chan struct{}),
		}
		done := make(chan struct{})
		go func() {
			s.superviseAgent(ap)
			close(done)
		}()

		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		shutdownClosed := false
		for {
			select {
			case <-done:
				if !shutdownClosed {
					t.Fatal("superviseAgent exited before spawn failure was observed")
				}
				if ap.StopReason != StopReasonShutdown {
					t.Fatalf("StopReason = %q, want shutdown", ap.StopReason)
				}
				return
			case <-ticker.C:
				ap.Mu.Lock()
				observedFailure := ap.RestartCount > 0 || ap.LastError != nil
				ap.Mu.Unlock()
				if observedFailure && !shutdownClosed {
					close(s.Shutdown)
					shutdownClosed = true
				}
			case <-time.After(2 * time.Second):
				if !shutdownClosed {
					close(s.Shutdown)
				}
				t.Fatal("superviseAgent did not exit after shutdown")
			}
		}
	})

	t.Run("state helpers and status snapshot", func(t *testing.T) {
		s := newTestSupervisorWithConfig(cfg)
		var emitted int
		s.EmitEvent = func(events.Event) { emitted++ }
		ap := &AgentProcess{
			Entry: cfgpkg.AgentEntry{
				Worktree: "nova",
				Role:     "task",
				Repo:     "api",
				Parent:   "epic-1",
				Backend:  "codex",
				Mode:     domain.AgentModeEphemeral,
			},
			RepoConfig:             &cfgpkg.RepoConfig{Remote: "upstream", DefaultBranch: "trunk"},
			WorktreePath:           t.TempDir(),
			Pid:                    123,
			RestartCount:           2,
			AssignedTaskID:         "task-1",
			AgentSessionID:         "session-1",
			AgentLeaseID:           "lease-1",
			AgentLeaseToken:        "token-1",
			TranscriptPath:         "transcript.jsonl",
			LogFilePath:            "agent.log",
			OwnershipLeaseID:       "owner-lease",
			OwnershipFencingToken:  7,
			OwnershipLastHeartbeat: time.Unix(10, 0),
			LastError:              &agenterr.AgentError{Class: agenterr.RateLimited, Backend: "codex", Message: "slow down"},
			StopCh:                 make(chan struct{}),
		}
		s.Agents = []*AgentProcess{ap}

		if epic := s.assignEpic(ap); epic != "epic-1" || emitted != 1 {
			t.Fatalf("assignEpic epic=%q emitted=%d", epic, emitted)
		}
		metadata := s.agentSessionMetadata(ap, "epic-2")
		for _, key := range []string{"backend", "epic_id", "task_id", "attempt_kind", "cleanup_state", "repo", "transcript_path", "log_path"} {
			if metadata[key] == "" {
				t.Fatalf("metadata missing %q: %+v", key, metadata)
			}
		}
		statuses := s.GetAgents()
		if len(statuses) != 1 || statuses[0].LastErrorClass == "" || statuses[0].RemoteBranch != "upstream/trunk" {
			t.Fatalf("statuses = %+v", statuses)
		}
		if s.AgentCount() != 1 {
			t.Fatalf("AgentCount = %d, want 1", s.AgentCount())
		}

		s.clearAgentSessionState(ap)
		if ap.AgentSessionID != "" || ap.AgentLeaseID != "" || ap.AssignedTaskID != "" || ap.TranscriptPath != "" {
			t.Fatalf("clearAgentSessionState left state: %+v", ap)
		}
		ap.StopReason = StopReasonManualStop
		s.setStopReasonDefault(ap, StopReasonConfigRemoved)
		if ap.StopReason != StopReasonManualStop {
			t.Fatalf("setStopReasonDefault overwrote reason: %q", ap.StopReason)
		}
	})

	t.Run("stop signal helpers", func(t *testing.T) {
		s := newTestSupervisorWithConfig(cfg)
		ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "nova"}, StopCh: make(chan struct{})}
		if s.checkAgentStopSignals(ap) {
			t.Fatal("default select should not stop")
		}
		close(ap.StopCh)
		if !s.checkAgentStopSignals(ap) || ap.StopReason != StopReasonConfigRemoved {
			t.Fatalf("stop signal reason = %q", ap.StopReason)
		}

		s = newTestSupervisorWithConfig(cfg)
		ap = &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "nova"}, StopCh: make(chan struct{})}
		close(s.Shutdown)
		if !s.checkAgentStopSignals(ap) || ap.StopReason != StopReasonShutdown {
			t.Fatalf("shutdown reason = %q", ap.StopReason)
		}
	})

	t.Run("restart sleep outcomes", func(t *testing.T) {
		s := newTestSupervisorWithConfig(cfg)
		s.EmitEvent = func(events.Event) {}
		ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "nova", Role: "task"}, StopCh: make(chan struct{})}
		if !s.sleepBeforeRestart(ap) {
			t.Fatal("zero backoff should continue")
		}
		if !ap.BackoffUntil.IsZero() {
			t.Fatalf("BackoffUntil not cleared: %v", ap.BackoffUntil)
		}

		one := 1
		s = newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{RestartPolicy: cfgpkg.RestartPolicy{
			BackoffInitial: &one,
			BackoffMax:     &one,
		}}})
		s.EmitEvent = func(events.Event) {}
		ap = &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "nova", Role: "task"}, StopCh: make(chan struct{})}
		close(ap.StopCh)
		if s.sleepBeforeRestart(ap) || ap.StopReason != StopReasonConfigRemoved {
			t.Fatalf("stop during backoff reason = %q", ap.StopReason)
		}

		s = newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{RestartPolicy: cfgpkg.RestartPolicy{
			BackoffInitial: &one,
			BackoffMax:     &one,
		}}})
		s.EmitEvent = func(events.Event) {}
		ap = &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "nova", Role: "task"}, StopCh: make(chan struct{})}
		close(s.Shutdown)
		if s.sleepBeforeRestart(ap) || ap.StopReason != StopReasonShutdown {
			t.Fatalf("shutdown during backoff reason = %q", ap.StopReason)
		}
	})

	t.Run("ownership retry stop paths and finalize helpers", func(t *testing.T) {
		s := newTestSupervisorWithConfig(cfg)
		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "nova", Role: "task"},
			WorktreePath: t.TempDir(),
			StopCh:       make(chan struct{}),
		}
		close(ap.StopCh)
		if s.sleepBeforeOwnershipRetry(ap) || ap.StopReason != StopReasonConfigRemoved || !ap.BackoffUntil.IsZero() {
			t.Fatalf("stop retry result reason=%q backoff=%v", ap.StopReason, ap.BackoffUntil)
		}

		s = newTestSupervisorWithConfig(cfg)
		ap = &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "spark", Role: "task"},
			WorktreePath: t.TempDir(),
			StopCh:       make(chan struct{}),
			LastError:    &agenterr.AgentError{Class: agenterr.Timeout},
		}
		close(s.Shutdown)
		if s.sleepBeforeOwnershipRetry(ap) || ap.StopReason != StopReasonShutdown || !ap.BackoffUntil.IsZero() {
			t.Fatalf("shutdown retry result reason=%q backoff=%v", ap.StopReason, ap.BackoffUntil)
		}
		errClass := agentErrorClass(ap)
		if errClass != agenterr.Timeout.String() {
			t.Fatalf("agentErrorClass = %q", errClass)
		}
		res := finalizeLocalSession(nil, ap, "HEAD", "TASK-1", 7, errClass)
		if res.DiffStats.FilesChanged != 0 || len(res.FilesTouched) != 0 || res.HasDiffPatch {
			t.Fatalf("finalizeLocalSession nil session result = %+v", res)
		}
		s.postExitCleanup(ap)
	})

	t.Run("node owner and daemon path helpers", func(t *testing.T) {
		t.Setenv("LOOM_FLEET_DB_ACTOR", "")
		t.Setenv("LOOM_AGENT_NAME", "")
		t.Setenv("USER", "")
		if got := resolveNodeOwnerActor(); got != "local" {
			t.Fatalf("resolveNodeOwnerActor = %q, want local", got)
		}
		abs := t.TempDir()
		if got := ResolveDaemonPath("/project", abs); got != abs {
			t.Fatalf("absolute daemon path = %q, want %q", got, abs)
		}
		if got := ResolveDaemonPath("/project", "logs/out.log"); got != "/project/logs/out.log" {
			t.Fatalf("relative daemon path = %q", got)
		}

		s := newTestSupervisorWithConfig(cfg)
		lockDir := t.TempDir()
		writeLockFile(t, lockDir, &cli.LockInfo{TaskID: "TASK-FROM-LOCK", StartedAt: time.Now()})
		ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "nova"}, WorktreePath: lockDir}
		if got := s.taskIDForFinalize(ap); got != "TASK-FROM-LOCK" {
			t.Fatalf("taskIDForFinalize lock = %q", got)
		}
		ap.AssignedTaskID = "TASK-FALLBACK"
		ap.WorktreePath = t.TempDir()
		if got := s.taskIDForFinalize(ap); got != "TASK-FALLBACK" {
			t.Fatalf("taskIDForFinalize fallback = %q", got)
		}
	})

	t.Run("control plane session lifecycle", func(t *testing.T) {
		ctx := context.Background()
		st := memstore.New()
		t.Cleanup(func() { _ = st.Close() })
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
			t.Fatalf("create workspace: %v", err)
		}
		s := newTestSupervisorWithConfig(cfg)
		s.ControlStore = st
		s.WorkspaceID = "WS"
		s.NodeID = "node-1"
		ap := &AgentProcess{
			Entry: cfgpkg.AgentEntry{
				Worktree: "nova",
				Role:     "task",
				Repo:     "api",
				Backend:  "codex",
				Mode:     domain.AgentModeEphemeral,
			},
			AssignedTaskID:  "TASK-1",
			AgentSessionID:  "session-1",
			ParentSessionID: "parent-session",
		}

		s.createControlPlaneAgentSession(ap, "session-1", "EPIC-1", "implementation", 2)
		if ap.AgentLeaseID == "" || ap.AgentLeaseToken == "" {
			t.Fatalf("agent lease was not stored on process: id=%q token=%q", ap.AgentLeaseID, ap.AgentLeaseToken)
		}
		s.markControlPlaneAgentSessionRunning(ap)
		session, err := st.AgentSessions().Get(ctx, "WS", "session-1")
		if err != nil {
			t.Fatalf("get running session: %v", err)
		}
		if session.Status != domain.AgentSessionRunning || session.Metadata["backend"] != "codex" {
			t.Fatalf("running session = %+v", session)
		}

		s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
			sessionID:  "session-1",
			leaseID:    ap.AgentLeaseID,
			leaseToken: ap.AgentLeaseToken,
			exitCode:   1,
			errClass:   "RateLimited",
			taskID:     "TASK-2",
		})
		session, err = st.AgentSessions().Get(ctx, "WS", "session-1")
		if err != nil {
			t.Fatalf("get completed session: %v", err)
		}
		if session.Status != domain.AgentSessionFailed || session.TaskID != "TASK-2" || session.ErrorClass != "RateLimited" {
			t.Fatalf("completed session = %+v", session)
		}
		released, err := st.AgentLeases().Get(ctx, "WS", ap.AgentLeaseID)
		if err != nil {
			t.Fatalf("get released lease: %v", err)
		}
		if released.Status != domain.AgentLeaseReleased {
			t.Fatalf("lease status = %s, want released", released.Status)
		}
	})

	t.Run("session creation and finalize branches", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		isolateSupervisorWorkspaceRuntimeDir(t)
		s := newTestSupervisorWithConfig(cfg)
		s.EmitEvent = func(events.Event) {}
		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "planner", Role: "plan"},
			WorktreePath: t.TempDir(),
			StopCh:       make(chan struct{}),
		}
		s.createAgentSession(ap, "EPIC-1")
		if ap.Session == nil || ap.AgentSessionID == "" || ap.TranscriptPath == "" {
			t.Fatalf("createAgentSession did not populate session state: %+v", ap)
		}

		ctx := context.Background()
		st := memstore.New()
		t.Cleanup(func() { _ = st.Close() })
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
			t.Fatalf("create workspace: %v", err)
		}
		if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
			WorkspaceKey: "WS",
			SessionID:    "finalize-session",
			AgentID:      "planner",
			NodeID:       "node-1",
			Kind:         domain.AgentSessionKindTask,
			Status:       domain.AgentSessionRunning,
		}); err != nil {
			t.Fatalf("create finalize session: %v", err)
		}
		lease, err := st.AgentLeases().Create(ctx, store.AgentLeaseCreate{
			WorkspaceKey: "WS",
			SessionID:    "finalize-session",
			LeaseID:      "finalize-lease",
			AgentID:      "planner",
			NodeID:       "node-1",
			TTL:          time.Minute,
		})
		if err != nil {
			t.Fatalf("create finalize lease: %v", err)
		}
		s.ControlStore = st
		s.WorkspaceID = "WS"
		s.NodeID = "node-1"
		ap.AgentSessionID = "finalize-session"
		ap.AgentLeaseID = lease.LeaseID
		ap.AgentLeaseToken = lease.Token
		ap.AssignedTaskID = "TASK-1"
		s.finalizeAgentSession(ap, 0)
		session, err := st.AgentSessions().Get(ctx, "WS", "finalize-session")
		if err != nil {
			t.Fatalf("get finalized session: %v", err)
		}
		if session.Status != domain.AgentSessionCompleted || session.TaskID != "TASK-1" {
			t.Fatalf("finalized session = %+v", session)
		}

		ap.AgentSessionID = ""
		s.markControlPlaneAgentSessionRunning(ap)
		s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
			sessionID:  "finalize-session",
			leaseID:    lease.LeaseID,
			leaseToken: "wrong-token",
			exitCode:   0,
		})
		s.createControlPlaneAgentSession(ap, "missing-workspace-session", "", "implementation", 0)
	})

	t.Run("post mortem recovery error branch", func(t *testing.T) {
		s := newTestSupervisorWithConfig(cfg)
		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "broken", Role: "task"},
			WorktreePath: filepath.Join(t.TempDir(), "missing-worktree"),
		}
		s.postMortemRecovery(ap, 1)
	})

	t.Run("spawn build failure finalizes control plane session", func(t *testing.T) {
		ctx := context.Background()
		st := memstore.New()
		t.Cleanup(func() { _ = st.Close() })
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
			t.Fatalf("create workspace: %v", err)
		}
		if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
			WorkspaceKey: "WS",
			SessionID:    "spawn-session",
			AgentID:      "custom",
			NodeID:       "node-1",
			Kind:         domain.AgentSessionKindTask,
			Status:       domain.AgentSessionStarting,
		}); err != nil {
			t.Fatalf("create agent session: %v", err)
		}
		lease, err := st.AgentLeases().Create(ctx, store.AgentLeaseCreate{
			WorkspaceKey: "WS",
			SessionID:    "spawn-session",
			LeaseID:      "spawn-session-lease",
			AgentID:      "custom",
			NodeID:       "node-1",
			TTL:          time.Minute,
		})
		if err != nil {
			t.Fatalf("create lease: %v", err)
		}
		s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{RestartPolicy: cfgpkg.RestartPolicy{
			MaxRetries: &zero,
		}}})
		s.ControlStore = st
		s.WorkspaceID = "WS"
		s.NodeID = "node-1"
		s.Concurrency = NewConcurrencyTracker(nil)
		ap := &AgentProcess{
			Entry: cfgpkg.AgentEntry{
				Worktree: "custom",
				Role:     "custom-role",
			},
			RoleConfig:      cfgpkg.RoleConfig{},
			WorktreePath:    t.TempDir(),
			AgentSessionID:  "spawn-session",
			AgentLeaseID:    lease.LeaseID,
			AgentLeaseToken: lease.Token,
			StopCh:          make(chan struct{}),
		}

		s.spawnAndWait(ap)
		session, err := st.AgentSessions().Get(ctx, "WS", "spawn-session")
		if err != nil {
			t.Fatalf("get finalized session: %v", err)
		}
		if session.Status != domain.AgentSessionFailed || session.ExitCode == nil || *session.ExitCode != -1 || session.ErrorClass != "spawn_failure" {
			t.Fatalf("spawn failure session = %+v", session)
		}
	})
}

type supervisorNoReadyBackend struct {
	backend.IssueBackend
}

func (supervisorNoReadyBackend) Ready(context.Context, backend.ReadyOpts) ([]backend.IssueData, error) {
	return nil, nil
}
