package supervisor

import (
	"context"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
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
