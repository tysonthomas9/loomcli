package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/events"
)

func TestWireSupervisorCallbacksAdditionalBranches(t *testing.T) {
	mockBackend := clitest.NewMockIssueBackend()
	sup := &supervisor.Supervisor{
		Repos: []cfgpkg.RepoConfig{{Name: "repo-a", Path: "/tmp/repo-a"}},
	}
	wireSupervisorCallbacks(sup, mockBackend)

	sup.EmitEvent(events.Event{})
	if got := sup.FindRepoConfig(""); got != nil {
		t.Fatalf("FindRepoConfig empty = %+v, want nil", got)
	}
	if got := sup.FindRepoConfig("missing"); got != nil {
		t.Fatalf("FindRepoConfig missing = %+v, want nil", got)
	}
	if got := sup.FindRepoConfig("repo-a"); got == nil || got.Path != "/tmp/repo-a" {
		t.Fatalf("FindRepoConfig repo-a = %+v", got)
	}

	mockBackend.ReadyResult = []backend.IssueData{{ID: "task-1"}}
	ready, err := sup.IssueBackendReady("EPIC-1")
	if err != nil || !ready {
		t.Fatalf("IssueBackendReady success = ready:%t err:%v", ready, err)
	}

	mockBackend.ReadyResult = nil
	mockBackend.ReadyErr = errors.New("backend unavailable")
	ready, err = sup.IssueBackendReady("EPIC-2")
	if err == nil || ready || !strings.Contains(err.Error(), "EPIC-2") {
		t.Fatalf("IssueBackendReady error = ready:%t err:%v", ready, err)
	}
}

func TestInitSupervisorAgentsSkipsStoppedAndReturnsNewAgentError(t *testing.T) {
	sup := &supervisor.Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{Roles: map[string]cfgpkg.RoleConfig{}}
		},
		FindRepoConfig: func(string) *cfgpkg.RepoConfig { return nil },
	}

	err := initSupervisorAgents(sup, []cfgpkg.AgentEntry{
		{Worktree: "stopped", Role: "task", DesiredState: "stopped"},
		{Worktree: "missing-worktree", Role: "task"},
	})
	if err == nil || !strings.Contains(err.Error(), "missing-worktree") {
		t.Fatalf("initSupervisorAgents err = %v, want missing worktree error", err)
	}
	if len(sup.Agents) != 0 {
		t.Fatalf("stopped agent should have been skipped, agents=%d", len(sup.Agents))
	}
}

func TestRecordCommandPollErrNoopAndError(t *testing.T) {
	_, span := startCommandPollSpan(context.Background())
	recordCommandPollErr(span, nil)
	recordCommandPollErr(span, errors.New("poll failed"))
	span.End()
}
