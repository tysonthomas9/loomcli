package supervisor

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// Replay of the real incident ledger.
//
// These are the kill events the running daemon actually recorded in the
// PUPPET workspace's .loom/daemon-quarantine.json, in timestamp order across
// all tasks (the file had grown to 29 events by the time this fixture was
// taken; the incident writeup cites the first 21). PUPPET-178 is the case
// that matters: already tester-approved, it collected five kills — four from
// the critic and one from the integrator — and reached the quarantine
// threshold, because the progress detector could see only a moved worktree
// HEAD or a changed Design/Notes hash, and a critic's artifact is neither.
type replayKill struct {
	at    string
	task  string
	agent string
	stop  string
	class string
}

var replayKills = []replayKill{
	{at: "2026-08-26T19:46:38.683931+02:00", task: "PUPPET-178", agent: "critic", stop: "run_duration_exceeded", class: "Timeout"},
	{at: "2026-08-26T19:47:08.684818+02:00", task: "PUPPET-179", agent: "integrator", stop: "watchdog", class: "Unknown"},
	{at: "2026-08-26T20:32:08.702149+02:00", task: "PUPPET-178", agent: "critic", stop: "run_duration_exceeded", class: "Timeout"},
	{at: "2026-08-26T20:42:38.713907+02:00", task: "PUPPET-179", agent: "integrator", stop: "watchdog", class: "Unknown"},
	{at: "2026-08-26T21:43:56.776169+02:00", task: "PUPPET-178", agent: "critic", stop: "run_duration_exceeded", class: "Timeout"},
	{at: "2026-08-26T22:42:09.379796+02:00", task: "PUPPET-178", agent: "critic", stop: "watchdog", class: "Unknown"},
	{at: "2026-08-27T03:13:05.452567+02:00", task: "PUPPET-180", agent: "critic", stop: "run_duration_exceeded", class: "Timeout"},
	{at: "2026-08-27T03:48:05.425902+02:00", task: "PUPPET-178", agent: "integrator", stop: "watchdog", class: "Unknown"},
	{at: "2026-08-27T03:49:05.423686+02:00", task: "PUPPET-180", agent: "decomposer", stop: "run_duration_exceeded", class: "Timeout"},
	{at: "2026-08-27T03:58:35.422428+02:00", task: "PUPPET-181", agent: "critic", stop: "run_duration_exceeded", class: "Timeout"},
	{at: "2026-08-27T04:23:05.427045+02:00", task: "PUPPET-180", agent: "worker", stop: "watchdog", class: "Unknown"},
	{at: "2026-08-27T04:56:35.449991+02:00", task: "PUPPET-180", agent: "tester", stop: "watchdog", class: "Unknown"},
	{at: "2026-08-27T05:46:15.48391+02:00", task: "PUPPET-184", agent: "tester", stop: "watchdog", class: "Unknown"},
	{at: "2026-08-27T07:03:45.138819+02:00", task: "PUPPET-190", agent: "tester", stop: "watchdog", class: "Unknown"},
	{at: "2026-08-27T07:12:15.138425+02:00", task: "PUPPET-184", agent: "integrator", stop: "watchdog", class: "Unknown"},
	{at: "2026-08-27T07:13:45.140351+02:00", task: "PUPPET-191", agent: "worker", stop: "run_duration_exceeded", class: "Timeout"},
	{at: "2026-08-27T07:50:45.214968+02:00", task: "PUPPET-194", agent: "planner", stop: "run_duration_exceeded", class: "Timeout"},
	{at: "2026-08-27T07:58:43.565554+02:00", task: "PUPPET-190", agent: "integrator", stop: "watchdog", class: "Unknown"},
	{at: "2026-08-27T08:12:07.396637+02:00", task: "PUPPET-191", agent: "tester", stop: "watchdog", class: "Unknown"},
	{at: "2026-08-27T08:47:07.3794+02:00", task: "PUPPET-194", agent: "planner", stop: "run_duration_exceeded", class: "Timeout"},
	{at: "2026-08-27T08:48:37.364404+02:00", task: "PUPPET-194", agent: "critic", stop: "run_duration_exceeded", class: "Timeout"},
	{at: "2026-08-27T08:57:07.362105+02:00", task: "PUPPET-194", agent: "decomposer", stop: "run_duration_exceeded", class: "Timeout"},
	{at: "2026-08-27T08:57:07.486788+02:00", task: "PUPPET-190", agent: "integrator", stop: "watchdog", class: "Unknown"},
	{at: "2026-08-27T09:43:37.278479+02:00", task: "PUPPET-194", agent: "worker", stop: "run_duration_exceeded", class: "Timeout"},
	{at: "2026-08-27T09:46:37.27179+02:00", task: "PUPPET-191", agent: "integrator", stop: "watchdog", class: "Unknown"},
	{at: "2026-08-27T11:06:07.307909+02:00", task: "PUPPET-195", agent: "critic", stop: "run_duration_exceeded", class: "Timeout"},
	{at: "2026-08-27T12:07:52.575729+02:00", task: "PUPPET-196", agent: "planner", stop: "run_duration_exceeded", class: "Timeout"},
	{at: "2026-08-27T12:09:52.572526+02:00", task: "PUPPET-198", agent: "worker-2", stop: "watchdog", class: "Unknown"},
	{at: "2026-08-27T12:13:22.546078+02:00", task: "PUPPET-194", agent: "integrator", stop: "watchdog", class: "Unknown"},
}

// leavesComment reports whether a reaped run of this role had already written
// its artifact as a COMMENT on the task. The review roles do: a critic posts a
// verdict, a tester posts an approval, an integrator posts the delivery note,
// a decomposer posts its split. A planner's artifact is a design/notes write
// and a worker's is a commit — a run of either that was reaped mid-work left
// nothing behind, which is what makes those kills genuine no-progress kills.
func leavesComment(agent string) bool {
	switch agent {
	case "critic", "tester", "integrator", "decomposer":
		return true
	default:
		return false
	}
}

// replayFingerprint is the progress detector under test, expressed as a
// function of the issue state so the replay can be run against the widened
// detector and against the pre-fix one for contrast.
type replayFingerprint func(*issueStub) issueBaseline

// widenedFingerprint is what this change ships: design, notes, comments, labels.
func widenedFingerprint(st *issueStub) issueBaseline {
	return issueBaselineOf(&backend.IssueDetailData{
		IssueData: backend.IssueData{Design: st.design, Notes: st.notes, Labels: st.labels},
		Comments:  st.comments,
	})
}

// designNotesOnlyFingerprint is the detector as it stood during the incident.
func designNotesOnlyFingerprint(st *issueStub) issueBaseline {
	return issueBaseline{designHash: hashIssueField(st.design), notesHash: hashIssueField(st.notes)}
}

// replayLedger feeds every recorded kill through the ledger in timestamp
// order and returns the highest consecutive no-progress count each task ever
// reached. Each comment-producing run's artifact is applied BEFORE its kill is
// recorded: the run posted its verdict and was reaped afterwards, so the
// comment is already visible to the GET the record hook makes at that exit.
func replayLedger(t *testing.T, fp replayFingerprint) map[string]int {
	t.Helper()
	q := &taskQuarantine{rec: make(map[string]*taskFailureRecord)}
	state := make(map[string]*issueStub)
	peak := make(map[string]int)
	nextCommentID := int64(1000)

	for _, k := range replayKills {
		at, err := time.Parse(time.RFC3339Nano, k.at)
		if err != nil {
			t.Fatalf("fixture timestamp %q: %v", k.at, err)
		}
		st := state[k.task]
		if st == nil {
			st = &issueStub{status: "open", design: k.task + "-design"}
			state[k.task] = st
		}
		if leavesComment(k.agent) {
			nextCommentID++
			st.addComment(nextCommentID)
		}
		ev := killEvent{At: at, Agent: k.agent, StopReason: k.stop, ErrClass: k.class}
		count, progressed := q.recordEligibleKill(k.task, ev, fp(st), true)
		if !progressed && count > peak[k.task] {
			peak[k.task] = count
		}
	}
	return peak
}

func TestQuarantineReplay_RealLedgerDoesNotQuarantinePuppet178(t *testing.T) {
	peak := replayLedger(t, widenedFingerprint)

	if got := peak["PUPPET-178"]; got >= defaultQuarantineThreshold {
		t.Fatalf("PUPPET-178 peak no-progress count = %d, want < %d "+
			"(every one of its kills followed a critic/integrator comment)",
			got, defaultQuarantineThreshold)
	}
	// TODO: add PUPPET-194 once the duration-kill exclusion lands (sibling
	// task, parts A/C of PUPPET-195). Its kills are run_duration_exceeded and
	// two of them are planner runs, which leave no comment behind.

	// The replay must still be capable of counting: a task whose runs left no
	// artifact at all keeps accumulating.
	if got := peak["PUPPET-194"]; got == 0 {
		t.Error("PUPPET-194 peak = 0; the replay stopped counting entirely, which would mean the breaker is disabled")
	}
}

func TestQuarantineReplay_PreFixDetectorQuarantinedPuppet178(t *testing.T) {
	// Contrast run: with the design/notes-only detector the same event stream
	// walks PUPPET-178 straight past the threshold. This is the regression the
	// widened baseline fixes, replayed from the real ledger.
	peak := replayLedger(t, designNotesOnlyFingerprint)

	if got := peak["PUPPET-178"]; got < defaultQuarantineThreshold {
		t.Fatalf("pre-fix detector peak for PUPPET-178 = %d, want >= %d "+
			"(the fixture no longer reproduces the incident)", got, defaultQuarantineThreshold)
	}
}
