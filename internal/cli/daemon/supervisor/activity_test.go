package supervisor

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestRecordAgentActivity_AdvancesTimestamp(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	s.Agents = []*AgentProcess{
		{Entry: config.AgentEntry{Worktree: "worker", Role: "task"}},
	}

	t0 := time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC)
	s.RecordAgentActivity("worker", t0)

	if got := s.Agents[0].LastActivity; !got.Equal(t0) {
		t.Errorf("LastActivity = %v, want %v", got, t0)
	}
}

func TestRecordAgentActivity_DoesNotRegressOnOutOfOrderHeartbeat(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	t1 := time.Date(2026, 5, 21, 10, 1, 0, 0, time.UTC)
	t0 := time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC)
	s.Agents = []*AgentProcess{
		{Entry: config.AgentEntry{Worktree: "worker", Role: "task"}, LastActivity: t1},
	}

	s.RecordAgentActivity("worker", t0)

	if got := s.Agents[0].LastActivity; !got.Equal(t1) {
		t.Errorf("LastActivity = %v, want %v (no regression)", got, t1)
	}
}

func TestRecordAgentActivity_IgnoresUnknownAgent(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	s.Agents = []*AgentProcess{
		{Entry: config.AgentEntry{Worktree: "worker", Role: "task"}},
	}

	s.RecordAgentActivity("ghost", time.Now())

	if got := s.Agents[0].LastActivity; !got.IsZero() {
		t.Errorf("LastActivity on unrelated agent = %v, want zero", got)
	}
}

func TestRecordAgentActivity_IgnoresZeroTimestamp(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	t1 := time.Date(2026, 5, 21, 10, 1, 0, 0, time.UTC)
	s.Agents = []*AgentProcess{
		{Entry: config.AgentEntry{Worktree: "worker", Role: "task"}, LastActivity: t1},
	}

	s.RecordAgentActivity("worker", time.Time{})

	if got := s.Agents[0].LastActivity; !got.Equal(t1) {
		t.Errorf("LastActivity = %v, want %v (zero-time heartbeat must be ignored)", got, t1)
	}
}

func TestGetAgents_SurfacesAssignedTaskIDAndLastActivity(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	at := time.Date(2026, 5, 21, 10, 5, 0, 0, time.UTC)
	s.Agents = []*AgentProcess{
		{
			Entry:          config.AgentEntry{Worktree: "worker", Role: "task"},
			AssignedTaskID: "LOOM-11",
			LastActivity:   at,
		},
	}

	statuses := s.GetAgents()
	if len(statuses) != 1 {
		t.Fatalf("len(GetAgents()) = %d, want 1", len(statuses))
	}
	if statuses[0].AssignedTaskID != "LOOM-11" {
		t.Errorf("AssignedTaskID = %q, want %q", statuses[0].AssignedTaskID, "LOOM-11")
	}
	if !statuses[0].LastActivity.Equal(at) {
		t.Errorf("LastActivity = %v, want %v", statuses[0].LastActivity, at)
	}
}

func TestClearAgentSessionState_ResetsLastActivity(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	at := time.Date(2026, 5, 21, 10, 5, 0, 0, time.UTC)
	ap := &AgentProcess{
		Entry:          config.AgentEntry{Worktree: "worker", Role: "task"},
		AssignedTaskID: "LOOM-11",
		LastActivity:   at,
	}

	s.clearAgentSessionState(ap)

	if !ap.LastActivity.IsZero() {
		t.Errorf("LastActivity = %v, want zero (must reset between supervision cycles)", ap.LastActivity)
	}
	if ap.AssignedTaskID != "" {
		t.Errorf("AssignedTaskID = %q, want empty (must reset between supervision cycles)", ap.AssignedTaskID)
	}
}
