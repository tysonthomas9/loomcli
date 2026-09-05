package supervisor

import (
	"errors"
	"strings"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// newAuthStopAgent builds an AgentProcess in its post-classifyAgentExit shape
// for a fatal exit: the task the agent held plus the classified error.
func newAuthStopAgent(name, taskID string, class wrapper.ErrorClass) *AgentProcess {
	return &AgentProcess{
		Entry:          config.AgentEntry{Worktree: name},
		AssignedTaskID: taskID,
		LastExitCode:   1,
		LastError:      &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(class), ExitCode: 1},
	}
}

// newAuthStopSupervisor wires a Supervisor with the given mock backend.
func newAuthStopSupervisor(mock *clitest.MockIssueBackend) *Supervisor {
	cfg := &config.DaemonConfig{}
	s := &Supervisor{ConfigSnapshot: func() *config.DaemonConfig { return cfg }}
	if mock != nil {
		s.IssueBackend = mock
	}
	return s
}

// addLabelArgs returns the (id, label) pair of the first AddLabel call.
func addLabelArgs(t *testing.T, mock *clitest.MockIssueBackend) (string, string) {
	t.Helper()
	for _, c := range mock.Calls {
		if c.Method != "AddLabel" {
			continue
		}
		if len(c.Args) != 2 {
			t.Fatalf("AddLabel recorded %d args, want 2", len(c.Args))
		}
		return c.Args[0].(string), c.Args[1].(string)
	}
	t.Fatal("no AddLabel call recorded")
	return "", ""
}

// addCommentParams returns the params of the first AddComment call.
func addCommentParams(t *testing.T, mock *clitest.MockIssueBackend) backend.CommentAddParams {
	t.Helper()
	for _, c := range mock.Calls {
		if c.Method != "AddComment" {
			continue
		}
		p, ok := c.Args[0].(backend.CommentAddParams)
		if !ok {
			t.Fatalf("AddComment arg is %T, want backend.CommentAddParams", c.Args[0])
		}
		return p
	}
	t.Fatal("no AddComment call recorded")
	return backend.CommentAddParams{}
}

// An AuthFailure fatal stop must leave a durable, queryable trace on the task
// the agent was holding -- the whole point of the ticket: the daemon log alone
// is invisible to the board.
func TestAuthFatalStop_LabelsAndCommentsHeldTask(t *testing.T) {
	mock := &clitest.MockIssueBackend{}
	s := newAuthStopSupervisor(mock)
	ap := newAuthStopAgent("worker", "PUPPET-176", wrapper.ErrAuth)

	if s.shouldRestart(ap) {
		t.Fatal("shouldRestart returned true for an auth fatal stop")
	}
	if ap.StopReason != StopReasonFatalError {
		t.Fatalf("StopReason = %q, want %q", ap.StopReason, StopReasonFatalError)
	}

	id, label := addLabelArgs(t, mock)
	if id != "PUPPET-176" || label != agentAuthStoppedLabel {
		t.Fatalf("AddLabel(%q, %q), want (%q, %q)", id, label, "PUPPET-176", agentAuthStoppedLabel)
	}

	p := addCommentParams(t, mock)
	if p.IssueID != "PUPPET-176" {
		t.Fatalf("comment IssueID = %q, want PUPPET-176", p.IssueID)
	}
	for _, want := range []string{
		"AGENT STOPPED",
		"AuthFailure",
		"desired_state_not_running",
		"loom agentdef start worker",
		agentAuthStoppedLabel,
	} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("comment text missing %q:\n%s", want, p.Text)
		}
	}
}

// Daemon-generated operational text is ASCII-only, like the quarantine
// timeline it sits beside.
func TestAuthStopComment_IsASCII(t *testing.T) {
	text := formatAuthStopComment("worker")
	for i, r := range text {
		if r > 127 {
			t.Fatalf("non-ASCII rune %q at byte %d in comment text", r, i)
		}
	}
}

// A billing stop is fatal too, but its remediation is different and the
// agent's login is not what broke: no auth signal on the task.
func TestBillingFatalStop_WritesNothing(t *testing.T) {
	mock := &clitest.MockIssueBackend{}
	s := newAuthStopSupervisor(mock)
	ap := newAuthStopAgent("worker", "PUPPET-176", wrapper.ErrBilling)

	if s.shouldRestart(ap) {
		t.Fatal("shouldRestart returned true for a billing fatal stop")
	}
	if mock.Called("AddLabel") || mock.Called("AddComment") {
		t.Fatalf("billing stop wrote to the board: %+v", mock.Calls)
	}
}

// Nothing to write back to when the agent held no task (it failed to
// authenticate before it ever claimed one).
func TestAuthFatalStop_NoHeldTask(t *testing.T) {
	mock := &clitest.MockIssueBackend{}
	s := newAuthStopSupervisor(mock)
	ap := newAuthStopAgent("worker", "", wrapper.ErrAuth)

	if s.shouldRestart(ap) {
		t.Fatal("shouldRestart returned true for an auth fatal stop")
	}
	if mock.Called("AddLabel") || mock.Called("AddComment") {
		t.Fatalf("wrote to the board with no task held: %+v", mock.Calls)
	}
}

// Both writes are best-effort and independent: a failed label must not cost
// the comment, and neither failure may change the stop decision.
func TestAuthFatalStop_LabelFailureStillComments(t *testing.T) {
	mock := &clitest.MockIssueBackend{AddLabelErr: errors.New("backend down")}
	s := newAuthStopSupervisor(mock)
	ap := newAuthStopAgent("worker", "PUPPET-176", wrapper.ErrAuth)

	if s.shouldRestart(ap) {
		t.Fatal("shouldRestart returned true for an auth fatal stop")
	}
	if !mock.Called("AddComment") {
		t.Fatal("comment skipped after the label write failed")
	}
}

// No backend wired (daemon running without an issue store): the stop still
// happens, and reporting is simply skipped.
func TestAuthFatalStop_NoBackend(t *testing.T) {
	s := newAuthStopSupervisor(nil)
	ap := newAuthStopAgent("worker", "PUPPET-176", wrapper.ErrAuth)

	if s.shouldRestart(ap) {
		t.Fatal("shouldRestart returned true for an auth fatal stop")
	}
}
