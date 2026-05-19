package supervisor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestConcurrencyTrackerTryReleaseCountsAndNil(t *testing.T) {
	var nilTracker *ConcurrencyTracker
	if !nilTracker.TryAcquire("any") {
		t.Fatal("nil TryAcquire should succeed")
	}
	nilTracker.Release("any")
	if nilTracker.ActiveCount("any") != 0 {
		t.Fatal("nil ActiveCount should be zero")
	}
	if counts := nilTracker.Counts(); len(counts) != 0 {
		t.Fatalf("nil Counts = %#v", counts)
	}
	nilTracker.Close()

	limit := 1
	ct := NewConcurrencyTracker(map[string]cfgpkg.RoleConfig{
		"task": {MaxConcurrency: &limit},
	})
	if !ct.TryAcquire("task") {
		t.Fatal("first TryAcquire should succeed")
	}
	if ct.TryAcquire("task") {
		t.Fatal("second TryAcquire should fail at limit")
	}
	counts := ct.Counts()
	if counts["task"] != 1 {
		t.Fatalf("Counts = %#v", counts)
	}
	counts["task"] = 99
	if ct.ActiveCount("task") != 1 {
		t.Fatal("Counts should return a copy")
	}
	ct.Release("task")
	if ct.ActiveCount("task") != 0 {
		t.Fatalf("count after release = %d", ct.ActiveCount("task"))
	}
	ct.Release("task")
	if ct.ActiveCount("task") != 0 {
		t.Fatalf("count after extra release = %d", ct.ActiveCount("task"))
	}
	ct.Close()
	ct.Close()
	if ct.TryAcquire("task") {
		t.Fatal("TryAcquire should fail after close")
	}
}

func TestMergeRoleConfigOverlaysAllSupportedFields(t *testing.T) {
	maxPriority := 2
	maxConcurrency := 3
	budget := 4.5
	base := cfgpkg.RoleConfig{
		Description: "base",
		TaskFilter:  "needs_plan",
		Backend:     "claude",
	}
	overlay := cfgpkg.RoleConfig{
		Description:    "overlay",
		PromptFile:     "ignored.md",
		Model:          "gpt",
		TaskFilter:     "any",
		Backend:        "codex",
		PathPatterns:   []string{"*.go"},
		Skills:         []string{"go"},
		MaxPriority:    &maxPriority,
		MaxConcurrency: &maxConcurrency,
		ReadOnly:       true,
		AllowedTools:   []string{"Read"},
		DeniedTools:    []string{"Write"},
		MaxBudgetUSD:   &budget,
	}

	got := MergeRoleConfig(base, overlay)
	if got.Description != "overlay" || got.TaskFilter != "any" || got.Backend != "codex" || got.Model != "gpt" {
		t.Fatalf("basic fields not overlaid: %+v", got)
	}
	if got.PromptFile != "" {
		t.Fatalf("PromptFile should not be merged for built-ins: %+v", got)
	}
	if got.MaxPriority == nil || *got.MaxPriority != maxPriority {
		t.Fatalf("MaxPriority = %#v", got.MaxPriority)
	}
	if got.MaxConcurrency == nil || *got.MaxConcurrency != maxConcurrency {
		t.Fatalf("MaxConcurrency = %#v", got.MaxConcurrency)
	}
	if !got.ReadOnly || got.PathPatterns[0] != "*.go" || got.Skills[0] != "go" {
		t.Fatalf("slice/bool fields not overlaid: %+v", got)
	}
	if got.AllowedTools[0] != "Read" || got.DeniedTools[0] != "Write" {
		t.Fatalf("tool fields not overlaid: %+v", got)
	}
}

func TestAgentRuntimeStateUpdatesControlStore(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS", Name: "task"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{WorkspaceKey: "WS", Name: "worker", RoleName: "task"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	s := &Supervisor{ControlStore: st, WorkspaceID: "WS"}
	ap := &AgentProcess{
		Entry: cfgpkg.AgentEntry{Worktree: "worker", Role: "task", Mode: domain.AgentModeEphemeral},
	}

	s.markAgentActive(ap)
	agent, err := st.Agents().Get(ctx, "WS", "worker")
	if err != nil {
		t.Fatalf("get active agent: %v", err)
	}
	if agent.State != domain.AgentStateActive {
		t.Fatalf("agent state = %q", agent.State)
	}

	ap.StopReason = StopReasonEphemeralDone
	s.markAgentStoppedOnExit(ap)
	agent, err = st.Agents().Get(ctx, "WS", "worker")
	if err != nil {
		t.Fatalf("get stopped agent: %v", err)
	}
	if agent.State != domain.AgentStateStopped || agent.DesiredState != domain.AgentDesiredStopped {
		t.Fatalf("agent after stop = %+v", agent)
	}

	s.updateAgentRuntimeState(nil, domain.AgentStateActive, nil)
	(&Supervisor{}).updateAgentRuntimeState(ap, domain.AgentStateActive, nil)
}

func TestSupervisorIdlePollIntervalDefaultAndCustom(t *testing.T) {
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
	if got := s.GetIdlePollInterval(); got != 30 {
		t.Fatalf("default idle poll interval = %d", got)
	}
	custom := 11
	s = newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{
		Daemon: cfgpkg.DaemonSettings{
			RestartPolicy: cfgpkg.RestartPolicy{IdlePollInterval: &custom},
		},
	})
	if got := s.GetIdlePollInterval(); got != custom {
		t.Fatalf("custom idle poll interval = %d", got)
	}
}

func TestSupervisorNewAgentResolvesAbsoluteWorktreeAndRole(t *testing.T) {
	worktree := t.TempDir()
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
	s.FindRepoConfig = func(string) *cfgpkg.RepoConfig { return nil }

	ap, err := s.NewAgent(cfgpkg.AgentEntry{
		Worktree: worktree,
		Role:     "task",
	}, 4)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if ap.WorktreePath != worktree {
		t.Fatalf("WorktreePath = %q, want %q", ap.WorktreePath, worktree)
	}
	if ap.RoleConfig.TaskFilter != "has_design" {
		t.Fatalf("role config = %+v, want built-in task role", ap.RoleConfig)
	}

	_, err = s.NewAgent(cfgpkg.AgentEntry{Worktree: worktree, Role: "missing-role"}, 5)
	if err == nil || !strings.Contains(err.Error(), "agent[5]") {
		t.Fatalf("NewAgent missing role err = %v", err)
	}
	_, err = s.NewAgent(cfgpkg.AgentEntry{Worktree: worktree + "-missing", Role: "task"}, 6)
	if err == nil || !strings.Contains(err.Error(), "path does not exist") {
		t.Fatalf("NewAgent missing path err = %v", err)
	}

	s.finalizeAgentSession(&AgentProcess{}, 0)
	s.postExitCleanup(&AgentProcess{})
}

func TestSupervisorAssignmentStopAndSessionStateHelpers(t *testing.T) {
	var emitted []events.Event
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{Backend: "codex"}
		},
		Shutdown: make(chan struct{}),
		EmitEvent: func(evt events.Event) {
			emitted = append(emitted, evt)
		},
	}
	ap := &AgentProcess{
		Entry: cfgpkg.AgentEntry{
			Worktree: "worker-1",
			Role:     "task",
			Repo:     "api",
			Parent:   "EPIC-1",
			Mode:     domain.AgentModeEphemeral,
		},
		StopCh:                 make(chan struct{}),
		Session:                nil,
		AgentSessionID:         "session-1",
		AgentLeaseID:           "lease-1",
		AgentLeaseToken:        "token",
		TranscriptPath:         "/tmp/transcript.jsonl",
		BeforeRef:              "HEAD",
		AssignedTaskID:         "TASK-1",
		LogFilePath:            "/tmp/agent.log",
		OwnershipLeaseID:       "own-1",
		OwnershipFencingToken:  7,
		OwnershipLastHeartbeat: time.Now(),
	}

	if got := s.assignEpic(ap); got != "EPIC-1" {
		t.Fatalf("assignEpic = %q", got)
	}
	if ap.AssignedEpicID != "EPIC-1" {
		t.Fatalf("AssignedEpicID = %q", ap.AssignedEpicID)
	}
	if len(emitted) != 1 || emitted[0].Type != events.EpicAssigned || emitted[0].EpicID != "EPIC-1" {
		t.Fatalf("emitted = %+v", emitted)
	}
	metadata := s.agentSessionMetadata(ap, "")
	if metadata["backend"] != "codex" || metadata["epic_id"] != "EPIC-1" || metadata["task_id"] != "TASK-1" ||
		metadata["attempt_kind"] != "ephemeral_task_attempt" || metadata["repo"] != "api" ||
		metadata["transcript_path"] != "/tmp/transcript.jsonl" || metadata["log_path"] != "/tmp/agent.log" {
		t.Fatalf("metadata = %#v", metadata)
	}

	s.clearAgentSessionState(ap)
	if ap.AgentSessionID != "" || ap.AgentLeaseID != "" || ap.AgentLeaseToken != "" ||
		ap.TranscriptPath != "" || ap.BeforeRef != "" || ap.AssignedTaskID != "" {
		t.Fatalf("session state was not cleared: %+v", ap)
	}
	if s.checkAgentStopSignals(ap) {
		t.Fatal("unexpected stop signal before channels close")
	}
	close(ap.StopCh)
	if !s.checkAgentStopSignals(ap) || ap.StopReason != StopReasonConfigRemoved {
		t.Fatalf("stop signal reason = %q", ap.StopReason)
	}
	s.setStopReasonDefault(ap, StopReasonManualStop)
	if ap.StopReason != StopReasonConfigRemoved {
		t.Fatalf("setStopReasonDefault overwrote reason: %q", ap.StopReason)
	}

	ap2 := &AgentProcess{Entry: ap.Entry, StopCh: make(chan struct{})}
	close(s.Shutdown)
	if !s.checkAgentStopSignals(ap2) || ap2.StopReason != StopReasonShutdown {
		t.Fatalf("shutdown reason = %q", ap2.StopReason)
	}
}

func TestSupervisorDrainReasonAndForcefulBranches(t *testing.T) {
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})

	fleetAP := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "fleet-agent"}}
	s.Agents = []*AgentProcess{fleetAP}
	if err := s.DrainAgentWithReason("fleet-agent", StopReasonManualStop); err != nil {
		t.Fatalf("DrainAgentWithReason fleet mode: %v", err)
	}
	if len(s.Agents) != 1 {
		t.Fatalf("fleet-mode drain should keep agent slice, len=%d", len(s.Agents))
	}
	if err := s.DrainAgentForceful("fleet-agent", StopReasonManualStop); err != nil {
		t.Fatalf("DrainAgentForceful fleet mode: %v", err)
	}

	done := make(chan struct{})
	close(done)
	withReason := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "with-reason"},
		WorktreePath: t.TempDir(),
		StopCh:       make(chan struct{}),
		Done:         done,
	}
	forceDone := make(chan struct{})
	close(forceDone)
	forceful := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "forceful"},
		WorktreePath: t.TempDir(),
		StopCh:       make(chan struct{}),
		Done:         forceDone,
	}
	s.Agents = []*AgentProcess{withReason, forceful}

	if err := s.DrainAgentWithReason("missing", StopReasonManualStop); err == nil {
		t.Fatal("DrainAgentWithReason missing agent error = nil")
	}
	if err := s.DrainAgentForceful("missing", StopReasonManualStop); err == nil {
		t.Fatal("DrainAgentForceful missing agent error = nil")
	}

	if err := s.DrainAgentWithReason("with-reason", StopReasonManualStop); err != nil {
		t.Fatalf("DrainAgentWithReason: %v", err)
	}
	if withReason.StopReason != StopReasonManualStop {
		t.Fatalf("withReason StopReason = %q", withReason.StopReason)
	}
	if len(s.Agents) != 1 || s.Agents[0] != forceful {
		t.Fatalf("agents after reason drain = %+v", s.Agents)
	}

	if err := s.DrainAgentForceful("forceful", StopReasonConfigRemoved); err != nil {
		t.Fatalf("DrainAgentForceful: %v", err)
	}
	if forceful.StopReason != StopReasonConfigRemoved {
		t.Fatalf("forceful StopReason = %q", forceful.StopReason)
	}
	if len(s.Agents) != 0 {
		t.Fatalf("agents after force drain len=%d, want 0", len(s.Agents))
	}
}

func TestSupervisorDrainAllCapsTimeoutsAndAddAgentGuards(t *testing.T) {
	long := 60
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{
		Daemon: cfgpkg.DaemonSettings{
			RestartPolicy: cfgpkg.RestartPolicy{
				YieldTimeout:   &long,
				SigtermTimeout: &long,
			},
		},
	})
	agents := []*AgentProcess{
		{Entry: cfgpkg.AgentEntry{Worktree: "one"}, WorktreePath: t.TempDir()},
		{Entry: cfgpkg.AgentEntry{Worktree: "two"}, WorktreePath: t.TempDir()},
	}
	s.drainAllWithGrace(agents)

	if err := s.AddAgentForTask(cfgpkg.AgentEntry{Worktree: "ephemeral", Mode: domain.AgentModeEphemeral}, ""); err == nil {
		t.Fatal("AddAgentForTask ephemeral without task error = nil")
	}
	s.Agents = []*AgentProcess{{Entry: cfgpkg.AgentEntry{Worktree: "dupe"}}}
	if err := s.AddAgentForTask(cfgpkg.AgentEntry{Worktree: "dupe"}, "TASK-1"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("AddAgentForTask duplicate error = %v", err)
	}

	s.FindRepoConfig = func(repoName string) *cfgpkg.RepoConfig {
		if repoName == "api" {
			return &cfgpkg.RepoConfig{Name: "api"}
		}
		return nil
	}
	ap := s.newRuntimeAgentProcess(
		cfgpkg.AgentEntry{Worktree: "worker", Role: "task", Repo: "api"},
		cfgpkg.RoleConfig{Backend: "codex"},
		t.TempDir(),
		"TASK-1",
		"parent-session",
	)
	if ap.StopCh == nil || ap.Done == nil || ap.RepoConfig == nil ||
		ap.RequestedTaskID != "TASK-1" || ap.ParentSessionID != "parent-session" {
		t.Fatalf("newRuntimeAgentProcess = %+v", ap)
	}
}

func TestSupervisorPreFlightSpawnFailureAndFinalizeFallbacks(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

	var emitted []events.Event
	cfg := &cfgpkg.DaemonConfig{
		Backend: "codex",
		Daemon: cfgpkg.DaemonSettings{
			EventsDir: ".loom/events",
			RestartPolicy: cfgpkg.RestartPolicy{
				MaxRetries: cfgpkg.IntPtr(0),
			},
		},
	}
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg },
		ProjectDir:     t.TempDir(),
		Shutdown:       make(chan struct{}),
		Concurrency:    NewConcurrencyTracker(nil),
		EmitEvent: func(evt events.Event) {
			emitted = append(emitted, evt)
		},
	}

	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker", Role: "custom", Parent: "EPIC-1"},
		RoleConfig:   cfgpkg.RoleConfig{PromptFile: "prompt.md"},
		WorktreePath: t.TempDir(),
	}
	if !s.preFlightSetup(ap) {
		t.Fatal("preFlightSetup returned false without an issue backend")
	}
	if ap.AssignedEpicID != "EPIC-1" || ap.AgentSessionID == "" {
		t.Fatalf("preflight state epic=%q session=%q", ap.AssignedEpicID, ap.AgentSessionID)
	}
	if len(emitted) != 1 || emitted[0].Type != events.EpicAssigned {
		t.Fatalf("emitted events = %+v", emitted)
	}

	bad := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "bad", Role: "custom"},
		RoleConfig:   cfgpkg.RoleConfig{},
		WorktreePath: t.TempDir(),
	}
	s.Concurrency.Acquire("custom")
	if s.spawnAndWait(bad) {
		t.Fatal("spawnAndWait returned true for missing custom prompt")
	}
	if bad.StopReason != StopReasonMaxRetries {
		t.Fatalf("spawn failure stop reason = %q", bad.StopReason)
	}

	finalizeAP := &AgentProcess{WorktreePath: t.TempDir(), AssignedTaskID: "TASK-1"}
	if got := s.taskIDForFinalize(finalizeAP); got != "TASK-1" {
		t.Fatalf("taskIDForFinalize fallback = %q", got)
	}
	yieldAP := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "yield"}, WorktreePath: t.TempDir()}
	if err := WriteYieldFile(yieldAP.WorktreePath, &YieldRequest{Reason: "manual"}); err != nil {
		t.Fatalf("WriteYieldFile: %v", err)
	}
	s.postMortemRecovery(yieldAP, 0)
}
