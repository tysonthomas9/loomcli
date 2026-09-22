package supervisor

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/taskcontent"
)

// bodylessDetail is the PUPPET-618 shape: a title and nothing else.
func bodylessDetail(id string) *backend.IssueDetailData {
	return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Title: "tester control subject", Status: "open"}}
}

func describedDetail(id string) *backend.IssueDetailData {
	return &backend.IssueDetailData{
		IssueData:   backend.IssueData{ID: id, Title: "real work", Status: "open"},
		Description: "there is something to do here",
	}
}

func plannerAgent() *AgentProcess {
	return &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "needs_plan"},
	}
}

func claimedIDs(mock *clitest.MockIssueBackend) []string {
	var out []string
	for _, c := range mock.Calls {
		if c.Method == "ClaimIssue" || c.Method == "ClaimIssueAsActor" {
			out = append(out, c.Args[0].(string))
		}
	}
	return out
}

func countCalls(mock *clitest.MockIssueBackend, method string) int {
	n := 0
	for _, c := range mock.Calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

func TestClaimTask_SkipsBodylessReadyTaskAndTakesNext(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "scratch", IssueType: "task", Status: "open", Priority: 0, Title: "tester control subject"},
		{ID: "real", IssueType: "task", Status: "open", Priority: 1, Title: "real work"},
	}
	mock.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
		if id == "real" {
			return describedDetail(id), nil
		}
		return bodylessDetail(id), nil
	}
	s := &Supervisor{IssueBackend: mock, ContentGate: taskcontent.NewGate()}
	ap := plannerAgent()

	if !s.claimTask(ap, "") {
		t.Fatal("claimTask returned false; the described task should have been claimed")
	}
	if ap.AssignedTaskID != "real" {
		t.Fatalf("AssignedTaskID = %q, want real", ap.AssignedTaskID)
	}
	for _, id := range claimedIDs(mock) {
		if id == "scratch" {
			t.Fatal("the bodyless task must never be claimed")
		}
	}
}

// TestClaimTask_RefusesBodylessRequestedTask is the regression test for the
// control-plane pre-assignment path the ticket is about: a row that arrives
// already targeted at an agent never passes through the ready-queue filter.
func TestClaimTask_RefusesBodylessRequestedTask(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "scratch", IssueType: "task", Status: "open", Priority: 0, Title: "tester control subject"},
	}
	mock.GetResult = bodylessDetail("scratch")
	s := &Supervisor{IssueBackend: mock, ContentGate: taskcontent.NewGate()}
	ap := plannerAgent()
	ap.RequestedTaskID = "scratch"

	if s.claimTask(ap, "") {
		t.Fatal("claimTask returned true for a bodyless requested task")
	}
	if len(claimedIDs(mock)) != 0 {
		t.Fatalf("claims = %v, want none", claimedIDs(mock))
	}
	if !ap.LastNoWork {
		t.Fatal("LastNoWork = false, want true")
	}
	if ap.LastError == nil || !strings.Contains(ap.LastError.Message, "no description or acceptance criteria") {
		t.Fatalf("LastError = %+v, want a message naming the missing content", ap.LastError)
	}
}

func TestClaimTask_AllowsWhenGetFails(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "task-1", IssueType: "task", Status: "open", Priority: 1, Title: "unknown content"},
	}
	mock.GetErr = context.DeadlineExceeded
	s := &Supervisor{IssueBackend: mock, ContentGate: taskcontent.NewGate()}
	ap := plannerAgent()

	if !s.claimTask(ap, "") {
		t.Fatal("claimTask returned false; the gate must fail open on a read failure")
	}
	if ap.AssignedTaskID != "task-1" {
		t.Fatalf("AssignedTaskID = %q, want task-1", ap.AssignedTaskID)
	}
}

func TestClaimTask_ResumeTaskNotGated(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.GetResult = bodylessDetail("resume-me")
	s := &Supervisor{IssueBackend: mock, ContentGate: taskcontent.NewGate()}
	ap := plannerAgent()
	ap.ResumeTaskID = "resume-me"

	if !s.claimTask(ap, "") {
		t.Fatal("claimTask returned false; resume of the agent's own task is exempt from the gate")
	}
	if ap.AssignedTaskID != "resume-me" {
		t.Fatalf("AssignedTaskID = %q, want resume-me", ap.AssignedTaskID)
	}
	if n := countCalls(mock, "Get"); n != 0 {
		t.Fatalf("Get called %d times on the resume path, want 0", n)
	}
}

func TestClaimTask_GateDisabledByEnv(t *testing.T) {
	t.Setenv(taskcontent.EnvKillSwitch, "off")
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "scratch", IssueType: "task", Status: "open", Priority: 0, Title: "tester control subject"},
	}
	mock.GetResult = bodylessDetail("scratch")
	s := &Supervisor{IssueBackend: mock, ContentGate: taskcontent.NewGate()}
	ap := plannerAgent()

	if !s.claimTask(ap, "") {
		t.Fatal("claimTask returned false with the gate disabled; pre-gate behavior must be restored")
	}
	if ap.AssignedTaskID != "scratch" {
		t.Fatalf("AssignedTaskID = %q, want scratch", ap.AssignedTaskID)
	}
}

func TestClaimTask_NilGateAllowsBodyless(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "scratch", IssueType: "task", Status: "open", Priority: 0, Title: "tester control subject"},
	}
	mock.GetResult = bodylessDetail("scratch")
	s := &Supervisor{IssueBackend: mock} // ContentGate deliberately unset
	ap := plannerAgent()

	if !s.claimTask(ap, "") {
		t.Fatal("a nil ContentGate must allow every claim")
	}
	if ap.AssignedTaskID != "scratch" {
		t.Fatalf("AssignedTaskID = %q, want scratch", ap.AssignedTaskID)
	}
}

func TestClaimTask_AllBodylessEndsInNoWork(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "scratch-1", IssueType: "task", Status: "open", Priority: 0, Title: "probe"},
		{ID: "scratch-2", IssueType: "task", Status: "open", Priority: 1, Title: "probe"},
	}
	mock.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
		return bodylessDetail(id), nil
	}
	s := &Supervisor{IssueBackend: mock, ContentGate: taskcontent.NewGate()}
	ap := plannerAgent()

	if s.claimTask(ap, "") {
		t.Fatal("claimTask returned true with every candidate bodyless")
	}
	if len(claimedIDs(mock)) != 0 {
		t.Fatalf("claims = %v, want none", claimedIDs(mock))
	}
	if !ap.LastNoWork || ap.LastError == nil || ap.LastError.Message != "no claimable tasks" {
		t.Fatalf("preflight = %+v, want the existing no-claimable-tasks NoWork", ap.LastError)
	}
	// One Get per candidate, memoized thereafter: the agent has no worktree
	// here, so the ready query runs once and each row is read exactly once.
	if n := countCalls(mock, "Get"); n != 2 {
		t.Fatalf("Get called %d times, want 2 (one per candidate, no spin)", n)
	}
}
