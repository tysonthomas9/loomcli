package supervisor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// timedOutAgent is an AgentProcess in the exact shape the exit path leaves after
// a run was killed for exceeding a ceiling: a lock file naming the task, a
// Timeout class, and a stop reason.
func timedOutAgent(t *testing.T, name, taskID string, reason StopReason) *AgentProcess {
	t.Helper()
	ap := newTaskAgent(t, name, taskID)
	ap.LastStart = time.Now().Add(-90 * time.Second)
	ap.StopReason = reason
	ap.LastError = &agenterr.AgentError{
		Class:    agenterr.OutcomeFromHarness(wrapper.ErrTimeout),
		ExitCode: 143,
	}
	return ap
}

// commentsFor is the mock's ListComments reply, built from what it was asked to
// add — enough to make the marker dedupe real across two recorder passes.
func recordedComments(mock *clitest.MockIssueBackend) []backend.CommentData {
	var out []backend.CommentData
	for _, text := range commentTexts(mock) {
		out = append(out, backend.CommentData{Text: text})
	}
	return out
}

// ---------------------------------------------------------------------------
// 1. the happy path
// ---------------------------------------------------------------------------

func TestRecordTimeoutRunWritesCommentAndLabels(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.GetResult = issueWithLabels("loom-9")
	s, _ := newAbandonedRunSupervisor(t, mock)
	ap := timedOutAgent(t, "coder-1", "loom-9", StopReasonRunDurationExceeded)

	s.recordTimeoutRun(ap, 143, "sess-1")

	comments := commentTexts(mock)
	if len(comments) != 1 {
		t.Fatalf("comments = %d, want 1: %v", len(comments), comments)
	}
	body := comments[0]
	for _, want := range []string{
		"Run hit a time ceiling",
		timeoutRunMarker + "sess-1",
		"| task | loom-9 |",
		"| agent | coder-1 |",
		"| exit code | 143 |",
		"run-duration cap",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("comment body missing %q:\n%s", want, body)
		}
	}
	labels := addedLabels(mock)
	if len(labels) != 2 {
		t.Fatalf("labels = %v, want the attempt counter and %s", labels, timeoutPartialLabel)
	}
	if labels[0] != attemptLabel("coder-1", 1) {
		t.Errorf("first label = %q, want %q (the counter lands first)", labels[0], attemptLabel("coder-1", 1))
	}
	if labels[1] != timeoutPartialLabel {
		t.Errorf("second label = %q, want %q", labels[1], timeoutPartialLabel)
	}
}

// ---------------------------------------------------------------------------
// 2. repeat behavior: bump on a new run, idempotent on the same one
// ---------------------------------------------------------------------------

func TestRecordTimeoutRunSecondRunBumpsTheCounter(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	// The board as the first pass left it.
	mock.GetResult = issueWithLabels("loom-9", attemptLabel("coder-1", 1), timeoutPartialLabel)
	mock.ListCommentsFn = func(_ context.Context, _ string) ([]backend.CommentData, error) {
		return []backend.CommentData{{Text: "earlier note " + timeoutRunMarker + "sess-1"}}, nil
	}
	s, _ := newAbandonedRunSupervisor(t, mock)
	ap := timedOutAgent(t, "coder-1", "loom-9", StopReasonWatchdog)

	s.recordTimeoutRun(ap, 137, "sess-2")

	if got := len(commentTexts(mock)); got != 1 {
		t.Fatalf("comments written = %d, want 1 (a different session is a different record)", got)
	}
	labels := addedLabels(mock)
	if len(labels) != 2 || labels[0] != attemptLabel("coder-1", 2) {
		t.Fatalf("labels = %v, want the counter at 2 plus %s", labels, timeoutPartialLabel)
	}
	if removed := removedLabels(mock); len(removed) != 1 || removed[0] != attemptLabel("coder-1", 1) {
		t.Fatalf("removed = %v, want the superseded =1 counter", removed)
	}
}

func TestRecordTimeoutRunIsIdempotentForTheSameRun(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.GetResult = issueWithLabels("loom-9")
	// The ticket as the recorder itself left it: the second pass reads back the
	// comment the first pass wrote, which is the whole dedupe mechanism.
	mock.ListCommentsFn = func(_ context.Context, _ string) ([]backend.CommentData, error) {
		return recordedComments(mock), nil
	}
	s, _ := newAbandonedRunSupervisor(t, mock)
	ap := timedOutAgent(t, "coder-1", "loom-9", StopReasonRunDurationExceeded)

	s.recordTimeoutRun(ap, 143, "sess-1")
	s.recordTimeoutRun(ap, 143, "sess-1")

	if got := len(commentTexts(mock)); got != 1 {
		t.Fatalf("comments = %d, want 1; the marker must collapse a replay", got)
	}
	if got, want := addedLabels(mock), []string{attemptLabel("coder-1", 1), timeoutPartialLabel}; len(got) != len(want) {
		t.Fatalf("labels = %v, want exactly %v; a replay must not re-label", got, want)
	}
}

// ---------------------------------------------------------------------------
// 3. everything that must write nothing
// ---------------------------------------------------------------------------

func TestRecordTimeoutRunIgnoresNonTimeoutExits(t *testing.T) {
	cases := map[string]*agenterr.AgentError{
		"clean exit":     nil,
		"spawn failure":  {Class: agenterr.OutcomeFromDomain(agenterr.SpawnFailureOutcome)},
		"incomplete run": {Class: agenterr.OutcomeFromDomain(agenterr.IncompleteRunOutcome)},
	}
	for name, lastErr := range cases {
		t.Run(name, func(t *testing.T) {
			mock := clitest.NewMockIssueBackend()
			mock.GetResult = issueWithLabels("loom-9")
			s, _ := newAbandonedRunSupervisor(t, mock)
			ap := timedOutAgent(t, "coder-1", "loom-9", StopReasonRunDurationExceeded)
			ap.LastError = lastErr

			s.recordTimeoutRun(ap, 1, "sess-1")

			if got := len(mock.Calls); got != 0 {
				t.Fatalf("backend calls = %d, want 0 for %s", got, name)
			}
		})
	}
}

func TestRecordTimeoutRunSkipsWhenNoTaskWasHeld(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	s, _ := newAbandonedRunSupervisor(t, mock)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "coder-1"},
		WorktreePath: t.TempDir(), // no lock file, no AssignedTaskID
	}
	ap.LastStart = time.Now()
	ap.StopReason = StopReasonRunDurationExceeded
	ap.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTimeout)}

	s.recordTimeoutRun(ap, 143, "sess-1")

	if got := len(mock.Calls); got != 0 {
		t.Fatalf("backend calls = %d, want 0 when the run held no task", got)
	}
}

func TestRecordTimeoutRunSkipsTerminalIssues(t *testing.T) {
	for _, status := range []string{"closed", "tombstone"} {
		t.Run(status, func(t *testing.T) {
			mock := clitest.NewMockIssueBackend()
			issue := issueWithLabels("loom-9")
			issue.Status = status
			mock.GetResult = issue
			s, _ := newAbandonedRunSupervisor(t, mock)
			ap := timedOutAgent(t, "coder-1", "loom-9", StopReasonRunDurationExceeded)

			s.recordTimeoutRun(ap, 143, "sess-1")

			if got := len(commentTexts(mock)); got != 0 {
				t.Errorf("comments = %d, want 0 on a %s issue", got, status)
			}
			if got := len(addedLabels(mock)); got != 0 {
				t.Errorf("labels = %d, want 0 on a %s issue", got, status)
			}
		})
	}
}

func TestRecordTimeoutRunRecordsOnBlockedIssues(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	issue := issueWithLabels("loom-9")
	issue.Status = "blocked"
	mock.GetResult = issue
	s, _ := newAbandonedRunSupervisor(t, mock)
	ap := timedOutAgent(t, "coder-1", "loom-9", StopReasonRunDurationExceeded)

	s.recordTimeoutRun(ap, 143, "sess-1")

	if got := len(commentTexts(mock)); got != 1 {
		t.Fatalf("comments = %d, want 1; blocked is not terminal", got)
	}
}

// ---------------------------------------------------------------------------
// 4. backend failures
// ---------------------------------------------------------------------------

func TestRecordTimeoutRunProceedsWhenListCommentsFails(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.GetResult = issueWithLabels("loom-9")
	mock.ListCommentsErr = errors.New("fleet-db is down")
	s, _ := newAbandonedRunSupervisor(t, mock)
	ap := timedOutAgent(t, "coder-1", "loom-9", StopReasonRunDurationExceeded)

	s.recordTimeoutRun(ap, 143, "sess-1")

	if got := len(commentTexts(mock)); got != 1 {
		t.Fatalf("comments = %d, want 1; a lost record is worse than a duplicate", got)
	}
}

func TestRecordTimeoutRunWritesNoLabelWhenTheCommentFails(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.GetResult = issueWithLabels("loom-9")
	mock.AddCommentErr = errors.New("body rejected")
	s, _ := newAbandonedRunSupervisor(t, mock)
	ap := timedOutAgent(t, "coder-1", "loom-9", StopReasonRunDurationExceeded)

	s.recordTimeoutRun(ap, 143, "sess-1")

	if got := addedLabels(mock); len(got) != 0 {
		t.Fatalf("labels = %v, want none: a label with no explanation is worse than neither", got)
	}
}

func TestRecordTimeoutRunSurvivesASideLabelFailure(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.GetResult = issueWithLabels("loom-9")
	mock.AddLabelFn = func(_ context.Context, _, label string) error {
		if label == timeoutPartialLabel {
			return errors.New("label rejected")
		}
		return nil
	}
	s, _ := newAbandonedRunSupervisor(t, mock)
	ap := timedOutAgent(t, "coder-1", "loom-9", StopReasonRunDurationExceeded)

	s.recordTimeoutRun(ap, 143, "sess-1")

	if got := len(commentTexts(mock)); got != 1 {
		t.Fatalf("comments = %d, want 1; the counter already landed", got)
	}
}

// ---------------------------------------------------------------------------
// 5. cause and ceiling rendering
// ---------------------------------------------------------------------------

func TestTimeoutRunCauseNamesTheCeilingThatWasCrossed(t *testing.T) {
	t.Setenv(envMaxRunDurationSeconds, "")
	t.Setenv("LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS", "")

	cases := []struct {
		name        string
		reason      StopReason
		roleCap     *int
		wantCause   string
		wantCeiling string
	}{
		{"run-duration cap from the role", StopReasonRunDurationExceeded, intPtr(60), "run-duration cap", "1m0s"},
		{"run-duration cap from the default", StopReasonRunDurationExceeded, nil, "run-duration cap", "4h0m0s"},
		{"cap disabled", StopReasonRunDurationExceeded, intPtr(0), "run-duration cap", "-"},
		{"silence watchdog", StopReasonWatchdog, nil, "output-timeout watchdog (silence)", "15m0s"},
		{"classified from the log", StopReason(""), nil, "harness-reported timeout", "-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := clitest.NewMockIssueBackend()
			mock.GetResult = issueWithLabels("loom-9")
			s, _ := newAbandonedRunSupervisor(t, mock)
			ap := timedOutAgent(t, "coder-1", "loom-9", tc.reason)
			ap.RoleConfig = cfgpkg.RoleConfig{MaxRunDuration: tc.roleCap}

			s.recordTimeoutRun(ap, 143, "sess-1")

			comments := commentTexts(mock)
			if len(comments) != 1 {
				t.Fatalf("comments = %d, want 1", len(comments))
			}
			if want := fmt.Sprintf("| cause | %s |", tc.wantCause); !strings.Contains(comments[0], want) {
				t.Errorf("body missing %q:\n%s", want, comments[0])
			}
			if want := fmt.Sprintf("| ceiling | %s |", tc.wantCeiling); !strings.Contains(comments[0], want) {
				t.Errorf("body missing %q:\n%s", want, comments[0])
			}
		})
	}
}

func intPtr(n int) *int { return &n }

// ---------------------------------------------------------------------------
// 6. the marker
// ---------------------------------------------------------------------------

func TestTimeoutRunIDFallsBackToTheAgentAndStart(t *testing.T) {
	ap := timedOutAgent(t, "coder-1", "loom-9", StopReasonRunDurationExceeded)
	id := timeoutRunID(ap, "")
	if !strings.HasPrefix(id, "coder-1@") {
		t.Fatalf("fallback id = %q, want it to name the agent", id)
	}
	if id == "" {
		t.Fatal("fallback id is empty; that marker would match every earlier comment")
	}

	other := timedOutAgent(t, "coder-1", "loom-9", StopReasonRunDurationExceeded)
	other.LastStart = ap.LastStart.Add(-2 * time.Hour)
	if timeoutRunID(other, "") == id {
		t.Fatal("two runs of the same agent produced the same fallback id")
	}
	if got := timeoutRunID(ap, "sess-1"); got != "sess-1" {
		t.Fatalf("id with a session = %q, want the session id", got)
	}
}

// ---------------------------------------------------------------------------
// 7. truncation
// ---------------------------------------------------------------------------

func TestTruncateCommentBodyKeepsTheMarker(t *testing.T) {
	marker := timeoutRunMarker + "sess-1"
	body := strings.Repeat("padding \u00e9\n", maxCommentBytes/4) + marker + "\n"

	got := truncateCommentBody(body)

	if len(got) > maxCommentBytes {
		t.Fatalf("len = %d, want <= %d", len(got), maxCommentBytes)
	}
	if !strings.Contains(got, marker) {
		t.Fatal("truncation dropped the marker; the record would re-comment forever")
	}
	if !strings.HasPrefix(got, "[truncated]\n") {
		t.Errorf("truncated body does not say so: %q", got[:20])
	}
	if !utf8.ValidString(got) {
		t.Error("truncation cut a rune in half")
	}
	short := "well under the cap"
	if truncateCommentBody(short) != short {
		t.Error("a body under the cap was rewritten")
	}
}

func TestRecordTimeoutRunTruncatesAnOversizedBody(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.GetResult = issueWithLabels("loom-9")
	s, _ := newAbandonedRunSupervisor(t, mock)
	ap := timedOutAgent(t, strings.Repeat("long-agent-name-", 1000), "loom-9", StopReasonRunDurationExceeded)

	s.recordTimeoutRun(ap, 143, "sess-1")

	comments := commentTexts(mock)
	if len(comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(comments))
	}
	if len(comments[0]) > maxCommentBytes {
		t.Fatalf("comment len = %d, want <= %d", len(comments[0]), maxCommentBytes)
	}
	if !strings.Contains(comments[0], timeoutRunMarker+"sess-1") {
		t.Fatal("the oversized body lost its marker")
	}
}
