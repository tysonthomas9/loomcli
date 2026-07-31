package daemon

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestDiffAgents_NoChanges(t *testing.T) {
	agents := []AgentEntry{
		{Worktree: "agent1", Role: "task"},
		{Worktree: "agent2", Role: "plan"},
	}
	added, removed, modified := diffAgents(agents, agents)
	if len(added) != 0 {
		t.Errorf("expected 0 added, got %d", len(added))
	}
	if len(removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(removed))
	}
	if len(modified) != 0 {
		t.Errorf("expected 0 modified, got %d", len(modified))
	}
}

func TestIncludeRoleConfigChanges_ReconcilesAssignedAgents(t *testing.T) {
	agents := []AgentEntry{{Worktree: "agent1", Role: "operator"}}

	t.Run("worker to interactive drains worker", func(t *testing.T) {
		oldRoles := map[string]RoleConfig{"operator": {Kind: "worker", PromptFile: "worker.md"}}
		newRoles := map[string]RoleConfig{"operator": {Kind: "interactive", PromptFile: "worker.md"}}
		got := includeRoleConfigChanges(oldRoles, newRoles, agents, agents, nil)
		if len(got) != 1 || got[0].Worktree != "agent1" {
			t.Fatalf("modified = %#v, want agent1", got)
		}
	})

	t.Run("interactive to worker starts worker", func(t *testing.T) {
		oldRoles := map[string]RoleConfig{"operator": {Kind: "interactive", PromptFile: "worker.md"}}
		newRoles := map[string]RoleConfig{"operator": {Kind: "worker", PromptFile: "worker.md"}}
		got := includeRoleConfigChanges(oldRoles, newRoles, agents, agents, nil)
		if len(got) != 1 || got[0].Worktree != "agent1" {
			t.Fatalf("modified = %#v, want agent1", got)
		}
	})

	t.Run("worker prompt change restarts worker", func(t *testing.T) {
		oldRoles := map[string]RoleConfig{"operator": {Kind: "worker", PromptFile: "old.md"}}
		newRoles := map[string]RoleConfig{"operator": {Kind: "worker", PromptFile: "new.md"}}
		got := includeRoleConfigChanges(oldRoles, newRoles, agents, agents, nil)
		if len(got) != 1 || got[0].Worktree != "agent1" {
			t.Fatalf("modified = %#v, want agent1", got)
		}
	})

	t.Run("interactive prompt change does not touch daemon", func(t *testing.T) {
		oldRoles := map[string]RoleConfig{"operator": {Kind: "interactive", PromptFile: "old.md"}}
		newRoles := map[string]RoleConfig{"operator": {Kind: "interactive", PromptFile: "new.md"}}
		if got := includeRoleConfigChanges(oldRoles, newRoles, agents, agents, nil); len(got) != 0 {
			t.Fatalf("modified = %#v, want none", got)
		}
	})
}

func TestDiffAgents_Added(t *testing.T) {
	old := []AgentEntry{
		{Worktree: "agent1", Role: "task"},
	}
	new := []AgentEntry{
		{Worktree: "agent1", Role: "task"},
		{Worktree: "agent2", Role: "plan"},
	}
	added, removed, modified := diffAgents(old, new)
	if len(added) != 1 || added[0].Worktree != "agent2" {
		t.Errorf("expected 1 added (agent2), got %v", added)
	}
	if len(removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(removed))
	}
	if len(modified) != 0 {
		t.Errorf("expected 0 modified, got %d", len(modified))
	}
}

func TestDiffAgents_Removed(t *testing.T) {
	old := []AgentEntry{
		{Worktree: "agent1", Role: "task"},
		{Worktree: "agent2", Role: "plan"},
	}
	new := []AgentEntry{
		{Worktree: "agent1", Role: "task"},
	}
	added, removed, modified := diffAgents(old, new)
	if len(added) != 0 {
		t.Errorf("expected 0 added, got %d", len(added))
	}
	if len(removed) != 1 || removed[0].Worktree != "agent2" {
		t.Errorf("expected 1 removed (agent2), got %v", removed)
	}
	if len(modified) != 0 {
		t.Errorf("expected 0 modified, got %d", len(modified))
	}
}

func TestDiffAgents_Modified(t *testing.T) {
	old := []AgentEntry{
		{Worktree: "agent1", Role: "task", Backend: "openai"},
	}
	new := []AgentEntry{
		{Worktree: "agent1", Role: "task", Backend: "anthropic"},
	}
	added, removed, modified := diffAgents(old, new)
	if len(added) != 0 {
		t.Errorf("expected 0 added, got %d", len(added))
	}
	if len(removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(removed))
	}
	if len(modified) != 1 || modified[0].Worktree != "agent1" {
		t.Errorf("expected 1 modified (agent1), got %v", modified)
	}
	if modified[0].Backend != "anthropic" {
		t.Errorf("expected modified entry to have new backend, got %s", modified[0].Backend)
	}
}

func TestDiffAgents_Mixed(t *testing.T) {
	old := []AgentEntry{
		{Worktree: "keep", Role: "task"},
		{Worktree: "remove", Role: "plan"},
		{Worktree: "change", Role: "task", Auto: false},
	}
	new := []AgentEntry{
		{Worktree: "keep", Role: "task"},
		{Worktree: "add", Role: "plan"},
		{Worktree: "change", Role: "task", Auto: true},
	}
	added, removed, modified := diffAgents(old, new)
	if len(added) != 1 {
		t.Errorf("expected 1 added, got %d", len(added))
	}
	if len(removed) != 1 {
		t.Errorf("expected 1 removed, got %d", len(removed))
	}
	if len(modified) != 1 {
		t.Errorf("expected 1 modified, got %d", len(modified))
	}
}

func TestDiffAgents_ModifiedPathPatterns(t *testing.T) {
	old := []AgentEntry{
		{Worktree: "agent1", Role: "task", PathPatterns: []string{"*.go"}},
	}
	new := []AgentEntry{
		{Worktree: "agent1", Role: "task", PathPatterns: []string{"*.go", "*.ts"}},
	}
	_, _, modified := diffAgents(old, new)
	if len(modified) != 1 {
		t.Errorf("expected 1 modified (path patterns changed), got %d", len(modified))
	}
}

func TestDiffAgents_ModifiedFallbackBackends(t *testing.T) {
	old := []AgentEntry{
		{Worktree: "agent1", Role: "task", FallbackBackends: []string{"fb1"}},
	}
	new := []AgentEntry{
		{Worktree: "agent1", Role: "task", FallbackBackends: []string{"fb1", "fb2"}},
	}
	_, _, modified := diffAgents(old, new)
	if len(modified) != 1 {
		t.Errorf("expected 1 modified (fallback backends changed), got %d", len(modified))
	}
}

func TestDiffAgents_EmptyOld(t *testing.T) {
	new := []AgentEntry{
		{Worktree: "agent1", Role: "task"},
	}
	added, removed, modified := diffAgents(nil, new)
	if len(added) != 1 {
		t.Errorf("expected 1 added, got %d", len(added))
	}
	if len(removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(removed))
	}
	if len(modified) != 0 {
		t.Errorf("expected 0 modified, got %d", len(modified))
	}
}

func TestDiffAgents_EmptyNew(t *testing.T) {
	old := []AgentEntry{
		{Worktree: "agent1", Role: "task"},
		{Worktree: "agent2", Role: "plan"},
	}
	added, removed, modified := diffAgents(old, nil)
	if len(added) != 0 {
		t.Errorf("expected 0 added, got %d", len(added))
	}
	if len(removed) != 2 {
		t.Errorf("expected 2 removed, got %d", len(removed))
	}
	if len(modified) != 0 {
		t.Errorf("expected 0 modified, got %d", len(modified))
	}
}

func TestDiffAgents_IgnoresSourceRepos(t *testing.T) {
	old := []AgentEntry{
		{Worktree: "agent1", Role: "task"},
	}
	new := []AgentEntry{
		{Worktree: "agent1", Role: "task", SourceRepos: []string{"repo-a", "repo-b"}},
	}
	added, removed, modified := diffAgents(old, new)
	if len(added) != 0 {
		t.Errorf("expected 0 added, got %d", len(added))
	}
	if len(removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(removed))
	}
	if len(modified) != 0 {
		t.Errorf("expected 0 modified, got %d: SourceRepos should be ignored", len(modified))
	}
}

func TestAgentEntry_Equal(t *testing.T) {
	base := AgentEntry{
		Worktree:         "w1",
		Role:             "task",
		Repo:             "backend",
		Auto:             true,
		Backend:          "anthropic",
		FallbackBackends: []string{"openai"},
		PathPatterns:     []string{"*.go"},
		Repos:            []string{"r1"},
		RepoGroups:       []string{"g1"},
		CrossRepo:        true,
		Parent:           "epic-1",
	}

	t.Run("identical", func(t *testing.T) {
		other := base
		if !base.Equal(other) {
			t.Error("expected Equal to return true for identical entries")
		}
	})

	t.Run("differs_only_in_SourceRepos", func(t *testing.T) {
		other := base
		other.SourceRepos = []string{"repo-a", "repo-b"}
		if !base.Equal(other) {
			t.Error("expected Equal to return true when only SourceRepos differs")
		}
	})

	t.Run("differs_Worktree", func(t *testing.T) {
		other := base
		other.Worktree = "w2"
		if base.Equal(other) {
			t.Error("expected Equal to return false when Worktree differs")
		}
	})

	t.Run("differs_Role", func(t *testing.T) {
		other := base
		other.Role = "plan"
		if base.Equal(other) {
			t.Error("expected Equal to return false when Role differs")
		}
	})

	t.Run("differs_Repo", func(t *testing.T) {
		other := base
		other.Repo = "frontend"
		if base.Equal(other) {
			t.Error("expected Equal to return false when Repo differs")
		}
	})

	t.Run("differs_Auto", func(t *testing.T) {
		other := base
		other.Auto = false
		if base.Equal(other) {
			t.Error("expected Equal to return false when Auto differs")
		}
	})

	t.Run("differs_Backend", func(t *testing.T) {
		other := base
		other.Backend = "openai"
		if base.Equal(other) {
			t.Error("expected Equal to return false when Backend differs")
		}
	})

	t.Run("differs_FallbackBackends", func(t *testing.T) {
		other := base
		other.FallbackBackends = []string{"openai", "azure"}
		if base.Equal(other) {
			t.Error("expected Equal to return false when FallbackBackends differs")
		}
	})

	t.Run("differs_PathPatterns", func(t *testing.T) {
		other := base
		other.PathPatterns = []string{"*.ts"}
		if base.Equal(other) {
			t.Error("expected Equal to return false when PathPatterns differs")
		}
	})

	t.Run("differs_Repos", func(t *testing.T) {
		other := base
		other.Repos = []string{"r1", "r2"}
		if base.Equal(other) {
			t.Error("expected Equal to return false when Repos differs")
		}
	})

	t.Run("differs_RepoGroups", func(t *testing.T) {
		other := base
		other.RepoGroups = []string{"g2"}
		if base.Equal(other) {
			t.Error("expected Equal to return false when RepoGroups differs")
		}
	})

	t.Run("differs_CrossRepo", func(t *testing.T) {
		other := base
		other.CrossRepo = false
		if base.Equal(other) {
			t.Error("expected Equal to return false when CrossRepo differs")
		}
	})

	t.Run("differs_Parent", func(t *testing.T) {
		other := base
		other.Parent = "epic-2"
		if base.Equal(other) {
			t.Error("expected Equal to return false when Parent differs")
		}
	})
}

func TestComputeConfigHash_Deterministic(t *testing.T) {
	dc := &DaemonConfig{
		Backend: "anthropic",
		Agents: []AgentEntry{
			{Worktree: "agent1", Role: "task"},
		},
	}
	h1 := computeConfigHash(dc)
	h2 := computeConfigHash(dc)
	if h1 != h2 {
		t.Errorf("expected deterministic hash, got %s and %s", h1, h2)
	}
	if h1 == "" {
		t.Error("expected non-empty hash")
	}
}

func TestComputeConfigHash_DifferentConfigs(t *testing.T) {
	dc1 := &DaemonConfig{
		Backend: "anthropic",
		Agents: []AgentEntry{
			{Worktree: "agent1", Role: "task"},
		},
	}
	dc2 := &DaemonConfig{
		Backend: "openai",
		Agents: []AgentEntry{
			{Worktree: "agent1", Role: "task"},
		},
	}
	h1 := computeConfigHash(dc1)
	h2 := computeConfigHash(dc2)
	if h1 == h2 {
		t.Error("expected different hashes for different configs")
	}
}

func TestComputeConfigHash_AgentChanges(t *testing.T) {
	dc1 := &DaemonConfig{
		Agents: []AgentEntry{
			{Worktree: "agent1", Role: "task"},
		},
	}
	dc2 := &DaemonConfig{
		Agents: []AgentEntry{
			{Worktree: "agent1", Role: "task"},
			{Worktree: "agent2", Role: "plan"},
		},
	}
	h1 := computeConfigHash(dc1)
	h2 := computeConfigHash(dc2)
	if h1 == h2 {
		t.Error("expected different hashes when agents differ")
	}
}

func TestModifiedAgentsToDrain_DefersActiveEphemeralTask(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "ws",
		Name:         "worker",
		RoleName:     "task",
		Mode:         domain.AgentModeEphemeral,
	}); err != nil {
		t.Fatalf("create worker: %v", err)
	}
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey:    "ws",
		TaskRunID:       "task-run-1",
		TaskID:          "TASK-1",
		WorkerProfileID: "worker",
		Status:          domain.TaskRunRunning,
	}); err != nil {
		t.Fatalf("create task run: %v", err)
	}

	d := &Daemon{
		sup: &supervisor.Supervisor{
			WorkspaceID: "ws",
		},
		store: st,
	}

	got := d.modifiedAgentsToDrain([]AgentEntry{{
		Worktree:     "worker",
		Role:         "task",
		Mode:         domain.AgentModeEphemeral,
		DesiredState: domain.AgentDesiredRunning,
	}})
	if len(got) != 0 {
		t.Fatalf("modifiedAgentsToDrain returned %d entries, want 0 while ephemeral task is active", len(got))
	}
}

func TestModifiedAgentsToDrain_DrainsCompletedEphemeralTask(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "ws",
		Name:         "worker",
		RoleName:     "task",
		Mode:         domain.AgentModeEphemeral,
	}); err != nil {
		t.Fatalf("create worker: %v", err)
	}
	stopped := domain.AgentStateStopped
	if _, err := st.Agents().Update(ctx, "ws", "worker", store.AgentUpdate{State: &stopped}); err != nil {
		t.Fatalf("stop worker: %v", err)
	}
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey:    "ws",
		TaskRunID:       "task-run-1",
		TaskID:          "TASK-1",
		WorkerProfileID: "worker",
		Status:          domain.TaskRunCompleted,
	}); err != nil {
		t.Fatalf("create completed task run: %v", err)
	}

	d := &Daemon{
		sup: &supervisor.Supervisor{
			WorkspaceID: "ws",
		},
		store: st,
	}

	entries := []AgentEntry{{
		Worktree:     "worker",
		Role:         "task",
		Mode:         domain.AgentModeEphemeral,
		DesiredState: domain.AgentDesiredStopped,
	}}
	got := d.modifiedAgentsToDrain(entries)
	if len(got) != 1 || got[0].Worktree != "worker" {
		t.Fatalf("modifiedAgentsToDrain = %#v, want completed worker to drain", got)
	}
}

func TestReconcilerLiveAgentCommandStatusUsesExplicitActiveStates(t *testing.T) {
	if liveAgentCommandStatus("") {
		t.Fatal("empty agent command status should not exist after queued became the durable default")
	}
	if !liveAgentCommandStatus(domain.AgentCommandAcked) {
		t.Fatal("acked agent command status should be live")
	}
	if liveAgentCommandStatus(domain.AgentCommandSucceeded) {
		t.Fatal("succeeded agent command status should not be live")
	}
}

func TestHasLiveForceStopCommand(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	cmd, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws",
		TargetAgentID: "worker",
		Type:          "stop",
		Payload:       map[string]string{"force": "true"},
	})
	if err != nil {
		t.Fatalf("create force-stop command: %v", err)
	}
	if _, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws",
		TargetAgentID: "other",
		Type:          "stop",
		Payload:       map[string]string{"force": "true"},
	}); err != nil {
		t.Fatalf("create other force-stop command: %v", err)
	}

	cfg := makeDaemonConfig([]AgentEntry{{Worktree: "worker"}}, nil)
	d := &Daemon{
		config: cfg,
		sup:    &supervisor.Supervisor{WorkspaceID: "ws", NodeID: "node-1"},
		store:  st,
	}
	got, err := d.hasLiveForceStopCommand("worker")
	if err != nil {
		t.Fatalf("hasLiveForceStopCommand: %v", err)
	}
	if !got {
		t.Fatal("hasLiveForceStopCommand = false, want true for queued force stop")
	}

	if _, err := st.AgentCommands().Ack(ctx, "ws", cmd.CommandID, store.AgentCommandAck{
		NodeID:  "node-1",
		OwnerID: "owner-1",
	}); err != nil {
		t.Fatalf("ack force-stop command: %v", err)
	}
	if _, err := st.AgentCommands().Complete(ctx, "ws", cmd.CommandID, store.AgentCommandComplete{
		NodeID: "node-1", OwnerID: "owner-1", Status: domain.AgentCommandSucceeded,
	}); err != nil {
		t.Fatalf("complete force-stop command: %v", err)
	}
	got, err = d.hasLiveForceStopCommand("worker")
	if err != nil {
		t.Fatalf("hasLiveForceStopCommand after completion: %v", err)
	}
	if got {
		t.Fatal("hasLiveForceStopCommand = true, want false after completion")
	}
}

func TestHasLiveForceStopCommand_PaginatesPastActiveHistory(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	for i := range 101 {
		if _, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
			WorkspaceKey:  "ws",
			TargetAgentID: "worker",
			Type:          "stop",
		}); err != nil {
			t.Fatalf("create non-force command %d: %v", i, err)
		}
	}
	if _, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws",
		TargetAgentID: "worker",
		Type:          "stop",
		Payload:       map[string]string{"force": "true"},
	}); err != nil {
		t.Fatalf("create force-stop command: %v", err)
	}

	cfg := makeDaemonConfig([]AgentEntry{{Worktree: "worker"}}, nil)
	d := &Daemon{
		config: cfg,
		sup:    &supervisor.Supervisor{WorkspaceID: "ws", NodeID: "node-1"},
		store:  st,
	}
	got, err := d.hasLiveForceStopCommand("worker")
	if err != nil {
		t.Fatalf("hasLiveForceStopCommand: %v", err)
	}
	if !got {
		t.Fatal("hasLiveForceStopCommand = false, want force stop after multiple pages")
	}
}

func TestHasLiveForceStopCommand_IgnoresOtherNode(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws",
		TargetAgentID: "worker",
		TargetNodeID:  "node-other",
		Type:          "stop",
		Payload:       map[string]string{"force": "true"},
	}); err != nil {
		t.Fatalf("create other-node force-stop command: %v", err)
	}

	cfg := makeDaemonConfig([]AgentEntry{{Worktree: "worker"}}, nil)
	d := &Daemon{
		config: cfg,
		sup:    &supervisor.Supervisor{WorkspaceID: "ws", NodeID: "node-1"},
		store:  st,
	}
	got, err := d.hasLiveForceStopCommand("worker")
	if err != nil {
		t.Fatalf("hasLiveForceStopCommand: %v", err)
	}
	if got {
		t.Fatal("hasLiveForceStopCommand = true, want other-node force intent ignored")
	}
}

func TestDrainAgentsHonorsQueuedForceStop(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "ws",
		TargetAgentID: "worker",
		Type:          "stop",
		Payload:       map[string]string{"force": "true"},
	}); err != nil {
		t.Fatalf("create force-stop command: %v", err)
	}

	entry := AgentEntry{
		Worktree:     "worker",
		Role:         "task",
		DesiredState: domain.AgentDesiredDraining,
	}
	ap := &supervisor.AgentProcess{
		Entry:  entry,
		StopCh: make(chan struct{}),
		Done:   make(chan struct{}),
	}
	go func() {
		<-ap.StopCh
		close(ap.Done)
	}()

	cfg := makeDaemonConfig([]AgentEntry{entry}, nil)
	d := &Daemon{
		config: cfg,
		store:  st,
		sup: &supervisor.Supervisor{
			ConfigSnapshot: func() *DaemonConfig { return cfg },
			WorkspaceID:    "ws",
			NodeID:         "node-1",
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			Agents:         []*supervisor.AgentProcess{ap},
		},
	}

	d.drainAgents([]AgentEntry{entry}, "modified")

	ap.Mu.Lock()
	reason := ap.StopReason
	ap.Mu.Unlock()
	if reason != supervisor.StopReasonManualStop {
		t.Fatalf("stop reason = %q, want force-stop reason %q", reason, supervisor.StopReasonManualStop)
	}
	if got := d.AgentCount(); got != 0 {
		t.Fatalf("AgentCount = %d after force drain, want 0", got)
	}
}

// TestConcurrentDrainAdd_Serialized verifies that the drainAddMu mutex
// serializes concurrent drainAgent calls, preventing double SIGTERM/SIGKILL
// races against the same agent process. Without serialization, two concurrent
// reloadAndReconcile calls could both find the same agent and drain it
// simultaneously (the bug described in loomcli-5y1sd.9).
func TestConcurrentDrainAdd_Serialized(t *testing.T) {
	t.Skip("test moved to supervisor package — tests supervisor.DrainAgent concurrency")
}

// TestConcurrentDrainAdd_AddNoDuplicate verifies the drainAddMu + agentsMu
// locking pattern that reloadAndReconcile uses during agent addition.
// It exercises the check+insert logic in isolation (not the full addAgent,
// which requires filesystem I/O via ResolveAgentTarget) to confirm that
// serialization via drainAddMu prevents duplicate agent entries.
func TestConcurrentDrainAdd_AddNoDuplicate(t *testing.T) {
	t.Skip("test moved to supervisor package — tests supervisor concurrent add/drain")
}
