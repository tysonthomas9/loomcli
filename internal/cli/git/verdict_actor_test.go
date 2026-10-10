package git

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

func humanEnv() map[string]string { return map[string]string{"USER": "tyson"} }

func leadEnv() map[string]string {
	return map[string]string{"USER": "tyson", "LOOM_AGENT_NAME": "lead-1", "LOOM_AGENT_TERMINAL_ID": "t1"}
}

// taskAgentEnv is a driver task run of task-1 by worker profile worker-1.
func taskAgentEnv() map[string]string {
	return map[string]string{"USER": "tyson", "LOOM_TASK_RUN_ID": "run-1", "LOOM_TASK_ID": "task-1",
		"LOOM_TASK_RUN_WORKER_PROFILE_ID": "worker-1", "LOOM_ORCHESTRATOR_SESSION_ID": "lead-session"}
}

// stubVerdictStore runs verdict commands in env against a store holding
// change-1 revision 2 (head head-2) of task-1, and change-2 revisions 1 and 3
// of task-1 in a second repo.
func stubVerdictStore(t *testing.T, env map[string]string) {
	t.Helper()
	oldEnv, oldTask, oldRevision, oldOwner := verdictEnv, verdictTaskRevisions, verdictRevision, verdictTaskForChange
	oldChange, oldNumber, oldRejectChange, oldRejectNumber := approveChange, approveRevision, rejectChange, rejectRevision
	t.Cleanup(func() {
		verdictEnv, verdictTaskRevisions, verdictRevision, verdictTaskForChange = oldEnv, oldTask, oldRevision, oldOwner
		approveChange, approveRevision, rejectChange, rejectRevision = oldChange, oldNumber, oldRejectChange, oldRejectNumber
	})
	approveChange, approveRevision, rejectChange, rejectRevision = "", 0, "", 0
	setVerdictEnv(env)
	verdictTaskRevisions = func(_ context.Context, workspace, task string) ([]review.TaskRevision, error) {
		if workspace != "workspace-1" || task != "task-1" {
			return nil, nil
		}
		return []review.TaskRevision{
			{ChangeID: "change-2", Repo: "web", Number: 3, HeadSHA: "head-c2-3"},
			{ChangeID: "change-1", Repo: "api", Number: 2, HeadSHA: "head-2"},
			{ChangeID: "change-2", Repo: "web", Number: 1, HeadSHA: "head-c2-1"},
		}, nil
	}
	verdictRevision = func(_ context.Context, _, change string, number int) (loomgit.Revision, error) {
		return loomgit.Revision{Change: change, Number: number, HeadSHA: "head-" + itoa(number)}, nil
	}
	verdictTaskForChange = func(context.Context, string, string) (string, error) { return "task-1", nil }
}

func setVerdictEnv(env map[string]string) {
	verdictEnv = func(name string) string { return env[name] }
}

func itoa(n int) string { return string(rune('0' + n)) }

func TestResolveCommandActorRecordsWhoeverRunsIt(t *testing.T) {
	old := verdictEnv
	t.Cleanup(func() { verdictEnv = old })
	for _, tc := range []struct {
		name     string
		env      map[string]string
		kind, id string
		task     string
	}{
		{"human shell", humanEnv(), "human", "tyson", ""},
		{"no user", map[string]string{}, "human", "local-user", ""},
		{"lead session", leadEnv(), "lead", "lead-1", ""},
		{"lead terminal without a name", map[string]string{"LOOM_AGENT_TERMINAL_ID": "t1"}, "lead", "lead", ""},
		{"orchestrated shell", map[string]string{"LOOM_ORCHESTRATOR_SESSION_ID": "s"}, "lead", "lead", ""},
		{"driver task agent", taskAgentEnv(), "agent", "worker-1", "task-1"},
		{"supervisor task worker", map[string]string{"LOOM_AGENT_NAME": "w2", "LOOM_ASSIGNED_TASK_ID": "task-9"}, "agent", "w2", "task-9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setVerdictEnv(tc.env)
			got := resolveCommandActor("lead")
			if got.Kind != tc.kind || got.ID != tc.id || got.Task != tc.task {
				t.Fatalf("actor=%+v, want %s %s task %q", got, tc.kind, tc.id, tc.task)
			}
		})
	}
}

// captureApprovals records the approvals the CLI asks for.
func captureApprovals(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	approveLocal = func(_ context.Context, _, lead, change string, revision int, headSHA string, actor review.Actor) (apply.FollowResult, error) {
		calls = append(calls, strings.Join([]string{lead, change, itoa(revision), headSHA, actor.Kind, actor.ID}, " "))
		return apply.FollowResult{Applied: []string{change}}, nil
	}
	return &calls
}

func TestApproveFromLeadSessionRecordsTheLead(t *testing.T) {
	stubApprovePublish(t, func(context.Context, string, string) ([]publish.ApprovalOutcome, error) { return nil, nil })
	setVerdictEnv(leadEnv())
	approveLead = "lead"
	calls := captureApprovals(t)
	runApproveForTest(t)
	if len(*calls) != 1 || (*calls)[0] != "lead-1 change-1 2 head-2 lead lead-1" {
		t.Fatalf("lead approval recorded as %v, want the lead lead-1 into its own working area", *calls)
	}
}

func TestApproveFromLeadSessionKeepsPolicyRefusal(t *testing.T) {
	stubApprovePublish(t, func(context.Context, string, string) ([]publish.ApprovalOutcome, error) {
		t.Fatal("a refused approval opened a PR")
		return nil, nil
	})
	setVerdictEnv(leadEnv())
	approveLocal = func(_ context.Context, _, _, _ string, _ int, _ string, actor review.Actor) (apply.FollowResult, error) {
		if actor.Kind != "lead" {
			t.Fatalf("lead approval recorded as %+v", actor)
		}
		return apply.FollowResult{}, loomgit.NewError(loomgit.ReviewRequired, "lead approval policy is off", nil)
	}
	err := runApprove(approveCmd, []string{"change-1", "2"})
	if err == nil || !strings.Contains(err.Error(), "lead approval policy is off") {
		t.Fatalf("lead approval with the policy off: %v", err)
	}
}

func TestApproveFromTaskAgentOnOwnTaskIsRefused(t *testing.T) {
	stubApprovePublish(t, func(context.Context, string, string) ([]publish.ApprovalOutcome, error) { return nil, nil })
	setVerdictEnv(taskAgentEnv())
	calls := captureApprovals(t)
	for _, args := range [][]string{{"task-1"}, {"change-1", "2"}} {
		err := runApprove(approveCmd, args)
		if err == nil || !strings.Contains(err.Error(), "a task agent cannot approve its own task task-1") {
			t.Fatalf("task agent approved its own task with %v: %v", args, err)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("a refused approval reached review: %v", *calls)
	}
}

func TestApproveFromTaskAgentOnAnotherTaskRecordsTheAgent(t *testing.T) {
	stubApprovePublish(t, func(context.Context, string, string) ([]publish.ApprovalOutcome, error) { return nil, nil })
	env := taskAgentEnv()
	env["LOOM_TASK_ID"] = "task-2"
	setVerdictEnv(env)
	calls := captureApprovals(t)
	runApproveForTest(t)
	if len(*calls) != 1 || (*calls)[0] != "lead-1 change-1 2 head-2 agent worker-1" {
		t.Fatalf("task agent approval recorded as %v, want agent worker-1", *calls)
	}
}

func TestApproveTaskPinsNewestRevisionOfEachRepo(t *testing.T) {
	stubApprovePublish(t, func(context.Context, string, string) ([]publish.ApprovalOutcome, error) { return nil, nil })
	calls := captureApprovals(t)
	var out bytes.Buffer
	approveCmd.SetOut(&out)
	t.Cleanup(func() { approveCmd.SetOut(nil) })
	if err := runApprove(approveCmd, []string{"task-1"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"lead-1 change-2 3 head-c2-3 human tyson", "lead-1 change-1 2 head-2 human tyson"}
	if strings.Join(*calls, "|") != strings.Join(want, "|") {
		t.Fatalf("approvals %v, want %v", *calls, want)
	}
	for _, line := range []string{"Approving as human tyson, for lead lead-1:", "change-2 revision 3 in web at head-c2-3", "change-1 revision 2 in api at head-2"} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("plan %q lacks %q", out.String(), line)
		}
	}
}

func TestApproveChangeFlagsAreTheAdvancedForm(t *testing.T) {
	stubApprovePublish(t, func(context.Context, string, string) ([]publish.ApprovalOutcome, error) { return nil, nil })
	calls := captureApprovals(t)
	approveChange, approveRevision = "change-1", 2
	if err := runApprove(approveCmd, nil); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0] != "lead-1 change-1 2 head-2 human tyson" {
		t.Fatalf("approvals %v", *calls)
	}
	approveChange, approveRevision = "change-1", 0
	if err := runApprove(approveCmd, nil); err == nil {
		t.Fatal("--change without --revision was accepted")
	}
}

func TestApproveRefusesStaleCode(t *testing.T) {
	stubApprovePublish(t, func(context.Context, string, string) ([]publish.ApprovalOutcome, error) {
		t.Fatal("a stale approval opened a PR")
		return nil, nil
	})
	for _, code := range []loomgit.Code{loomgit.RevisionSuperseded, loomgit.StaleSubject} {
		approveLocal = func(context.Context, string, string, string, int, string, review.Actor) (apply.FollowResult, error) {
			// A new attempt arrived after the plan was printed.
			return apply.FollowResult{}, loomgit.NewError(code, "a newer source revision exists", nil)
		}
		err := runApprove(approveCmd, []string{"task-1"})
		if err == nil || !strings.HasPrefix(err.Error(), "stale: change-2 revision 3 at head-c2-3") || !strings.Contains(err.Error(), "nothing was recorded") {
			t.Fatalf("%s: stale approval was not refused as stale: %v", code, err)
		}
	}
}

func TestRejectRecordsWhoeverRunsIt(t *testing.T) {
	oldReject, oldResolver := rejectLocal, rejectResolver
	t.Cleanup(func() { rejectLocal, rejectResolver = oldReject, oldResolver })
	stubApprovePublish(t, func(context.Context, string, string) ([]publish.ApprovalOutcome, error) { return nil, nil })
	rejectResolver = approveResolver
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"human", humanEnv(), "lead change-1 2 head-2 fix it human tyson"},
		{"lead", leadEnv(), "lead-1 change-1 2 head-2 fix it lead lead-1"},
		{"task agent", taskAgentEnv(), "lead change-1 2 head-2 fix it agent worker-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setVerdictEnv(tc.env)
			var calls []string
			rejectLocal = func(_ context.Context, _, lead, change string, revision int, headSHA, reason string, actor review.Actor) error {
				calls = append(calls, strings.Join([]string{lead, change, itoa(revision), headSHA, reason, actor.Kind, actor.ID}, " "))
				return nil
			}
			rejectReason = "fix it"
			t.Cleanup(func() { rejectReason = "" })
			cmd := *rejectCmd
			cmd.SetOut(&bytes.Buffer{})
			if err := runReject(&cmd, []string{"change-1", "2"}); err != nil {
				t.Fatal(err)
			}
			if len(calls) != 1 || calls[0] != tc.want {
				t.Fatalf("reject recorded %v, want %s", calls, tc.want)
			}
		})
	}
}

func TestRequestMergeFromTaskAgentRecordsTheAgent(t *testing.T) {
	stubMergeRequestCommands(t)
	old := verdictEnv
	t.Cleanup(func() { verdictEnv = old })
	setVerdictEnv(taskAgentEnv())
	var got publish.MergeActor
	prRequestMerge = func(_ context.Context, _, _, _, _ string, requester publish.MergeActor) (publish.MergeRequestView, error) {
		got = requester
		return publish.MergeRequestView{}, nil
	}
	cmd := *requestMergeCmd
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.RunE(&cmd, []string{"feature", "L", "C"}); err != nil {
		t.Fatal(err)
	}
	if got.Kind != "agent" || got.ID != "worker-1" {
		t.Fatalf("task agent merge request recorded as %+v", got)
	}
}

func TestConfirmMergeRefusesTaskAgent(t *testing.T) {
	old := verdictEnv
	t.Cleanup(func() { verdictEnv = old })
	setVerdictEnv(map[string]string{"USER": "tyson", "LOOM_TASK_RUN_ID": "run-1"})
	if _, err := humanMergeActor(); err == nil || !strings.Contains(err.Error(), "LOOM_TASK_RUN_ID") {
		t.Fatalf("task agent confirmed a merge: %v", err)
	}
}
