package supervisor

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestFallbackClaimYieldsToIdleSkilledPeer(t *testing.T) {
	s, mock, frontend, backendAgent := newSkillYieldFixture()
	task := skillYieldTask("backend-task", []string{"backend"}, 1)

	claimed, failed := s.tryClaimBestTask(frontend, []backend.IssueData{task}, s.buildClaimConstraints(frontend))
	if claimed || failed {
		t.Fatalf("frontend result = claimed %v, failed %v; want no decision", claimed, failed)
	}
	if frontend.AssignedTaskID != "" || frontend.LastError != nil {
		t.Fatalf("frontend state = task %q error %#v, want untouched", frontend.AssignedTaskID, frontend.LastError)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("frontend backend calls = %#v, want no claim", mock.Calls)
	}

	claimed, failed = s.tryClaimBestTask(backendAgent, []backend.IssueData{task}, s.buildClaimConstraints(backendAgent))
	if !claimed || failed || backendAgent.AssignedTaskID != task.ID {
		t.Fatalf("backend result = claimed %v, failed %v, task %q", claimed, failed, backendAgent.AssignedTaskID)
	}
}

func TestFallbackClaimDoesNotYieldToBusyPeer(t *testing.T) {
	s, _, frontend, backendAgent := newSkillYieldFixture()
	backendAgent.Pid = 1234
	task := skillYieldTask("backend-task", []string{"backend"}, 1)

	claimed, failed := s.tryClaimBestTask(frontend, []backend.IssueData{task}, s.buildClaimConstraints(frontend))
	if !claimed || failed || frontend.AssignedTaskID != task.ID {
		t.Fatalf("frontend result = claimed %v, failed %v, task %q", claimed, failed, frontend.AssignedTaskID)
	}
}

func TestFallbackClaimStopsYieldingAfterGrace(t *testing.T) {
	oldGrace := skillYieldGrace
	skillYieldGrace = 0
	t.Cleanup(func() { skillYieldGrace = oldGrace })

	s, _, frontend, _ := newSkillYieldFixture()
	task := skillYieldTask("backend-task", []string{"backend"}, 1)
	claimed, failed := s.tryClaimBestTask(frontend, []backend.IssueData{task}, s.buildClaimConstraints(frontend))
	if !claimed || failed || frontend.AssignedTaskID != task.ID {
		t.Fatalf("frontend result = claimed %v, failed %v, task %q", claimed, failed, frontend.AssignedTaskID)
	}
}

func TestFallbackClaimYieldsThenClaimsAnotherTask(t *testing.T) {
	s, _, frontend, _ := newSkillYieldFixture()
	issues := []backend.IssueData{
		skillYieldTask("backend-task", []string{"backend"}, 1),
		skillYieldTask("general-task", nil, 2),
	}

	claimed, failed := s.tryClaimBestTask(frontend, issues, s.buildClaimConstraints(frontend))
	if !claimed || failed || frontend.AssignedTaskID != "general-task" {
		t.Fatalf("frontend result = claimed %v, failed %v, task %q", claimed, failed, frontend.AssignedTaskID)
	}
}

func TestFallbackClaimWithoutSkilledPeerClaimsImmediately(t *testing.T) {
	s, _, frontend, backendAgent := newSkillYieldFixture()
	backendAgent.RoleConfig.Skills = []string{"database"}
	task := skillYieldTask("backend-task", []string{"backend"}, 1)

	claimed, failed := s.tryClaimBestTask(frontend, []backend.IssueData{task}, s.buildClaimConstraints(frontend))
	if !claimed || failed || frontend.AssignedTaskID != task.ID {
		t.Fatalf("frontend result = claimed %v, failed %v, task %q", claimed, failed, frontend.AssignedTaskID)
	}
}

func newSkillYieldFixture() (*Supervisor, *clitest.MockIssueBackend, *AgentProcess, *AgentProcess) {
	mock := clitest.NewMockIssueBackend()
	frontend := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "frontend-dev-1", Role: "frontend-dev"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design", Skills: []string{"frontend", "ui"}},
	}
	backendAgent := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "backend-dev-1", Role: "backend-dev"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design", Skills: []string{"backend", "api"}},
	}
	s := &Supervisor{
		Agents:        []*AgentProcess{frontend, backendAgent},
		StoppedAgents: make(map[string]struct{}),
		IssueBackend:  mock,
	}
	return s, mock, frontend, backendAgent
}

func skillYieldTask(id string, labels []string, priority int) backend.IssueData {
	return backend.IssueData{
		ID:        id,
		IssueType: "task",
		Status:    "open",
		Priority:  priority,
		Title:     id,
		Design:    "ready design",
		Labels:    labels,
	}
}
