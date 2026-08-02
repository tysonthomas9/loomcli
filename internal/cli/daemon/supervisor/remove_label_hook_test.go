package supervisor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// labelOpRecorder records every hook write in issue order AND the task each one
// targeted. The task id matters as much as the sequence here: a removal aimed at
// the wrong issue would strip a label off somebody else's task and still look
// like a clean run, so the id is asserted rather than assumed.
type labelOpRecorder struct {
	*clitest.MockIssueBackend
	mu       sync.Mutex
	sequence []string
	targets  []string
}

func newLabelOpRecorder(removeErr error) *labelOpRecorder {
	r := &labelOpRecorder{MockIssueBackend: clitest.NewMockIssueBackend()}
	r.AddCommentFn = func(_ context.Context, p backend.CommentAddParams) (*backend.CommentData, error) {
		r.record("comment", p.IssueID)
		return &backend.CommentData{IssueID: p.IssueID, Text: p.Text}, nil
	}
	r.AddLabelFn = func(_ context.Context, id string, l string) error {
		r.record("add:"+l, id)
		return nil
	}
	r.RemoveLabelFn = func(_ context.Context, id string, l string) error {
		r.record("remove:"+l, id)
		return removeErr
	}
	return r
}

func (r *labelOpRecorder) record(op, taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequence = append(r.sequence, op)
	r.targets = append(r.targets, taskID)
}

func (r *labelOpRecorder) seq() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sequence...)
}

func (r *labelOpRecorder) targetsSeen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.targets...)
}

// removeLabelSession lays down a real on-disk transcript so a pipeline that
// starts with a comment can run through the production entry point.
func removeLabelSession(t *testing.T) *sessions.Session {
	t.Helper()
	store := hookSessionRuntime(t)
	sess, err := store.CreateSession(sessions.CreateOptions{AgentName: "critic", Backend: backendnames.Codex})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	syncCanonicalTranscript(t, store, sess.SessionID(), canonicalReplyTranscript)
	return sess
}

// TestRunCompletionHooks_RemoveLabelOrdering drives the PRODUCTION entry point
// over comment → remove → stamp, the order hooksFromFlags builds.
//
// The removal must land after the artifact (it changes routing state, so
// write-before-stamp binds it) and before the add_label (the certifying write
// the next stage waits on — stamping first would leave the task carrying both
// the label that routed it here and the label that hands it on, claimable by
// two stages at once).
func TestRunCompletionHooks_RemoveLabelOrdering(t *testing.T) {
	sess := removeLabelSession(t)

	hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
		{Type: domain.AgentHookActionRemoveLabel, Value: "needs-review"},
		{Type: domain.AgentHookActionAddLabel, Value: "reviewed"},
	}}
	ap := newHookAgentProcess(t, "T-7", hooks)
	ap.AgentSessionID = sess.SessionID()
	r := newLabelOpRecorder(nil)
	s := &Supervisor{IssueBackend: r}

	if got := s.runCompletionHooks(ap, 0); got != 0 {
		t.Fatalf("exit code = %d, want the clean exit preserved", got)
	}
	if ap.LastError != nil {
		t.Fatalf("LastError = %+v, want nil for a fully applied pipeline", ap.LastError)
	}
	want := []string{"comment", "remove:needs-review", "add:reviewed"}
	if got := r.seq(); !equalStrings(got, want) {
		t.Fatalf("write order = %v, want %v", got, want)
	}
	// Every write, the removal included, targets the task this run owned.
	for i, target := range r.targetsSeen() {
		if target != "T-7" {
			t.Errorf("write %d (%s) targeted %q, want the owned task T-7", i, r.seq()[i], target)
		}
	}
}

// A removal is a real write, so its failure has to demote the run the way a
// failed stamp does: the pipeline stops at the first error, the certifying
// add_label is never attempted, and the task is reopened for a bounded retry.
// Continuing past it would publish a hand-off for work whose routing token was
// never consumed.
func TestRunCompletionHooks_RemoveLabelFailureDemotesTheRun(t *testing.T) {
	hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionRemoveLabel, Value: "needs-review"},
		{Type: domain.AgentHookActionAddLabel, Value: "reviewed"},
	}}
	ap := newHookAgentProcess(t, "T-1", hooks)
	ap.AgentSessionID = "sess-1"
	r := newLabelOpRecorder(errors.New("remove boom"))
	s := &Supervisor{IssueBackend: r}

	if got := s.runCompletionHooks(ap, 0); got != -1 {
		t.Fatalf("exit code = %d, want -1 for a failed removal", got)
	}
	if ap.LastError == nil ||
		ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.CompletionHookFailureOutcome) {
		t.Fatalf("LastError = %+v, want CompletionHookFailure", ap.LastError)
	}
	if !strings.Contains(ap.LastError.Message, "remove_label") {
		t.Errorf("LastError.Message = %q, want it to name the failed action", ap.LastError.Message)
	}
	if got := r.seq(); !equalStrings(got, []string{"remove:needs-review"}) {
		t.Fatalf("write sequence = %v, want the pipeline to stop at the failed removal", got)
	}
}

// Removals run in stored order alongside the stamps, and several of them are
// applied one by one — the supervisor never batches or reorders what the
// definition spelled out.
func TestRunCompletionHooks_SeveralRemovalsRunInStoredOrder(t *testing.T) {
	hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionRemoveLabel, Value: "needs-review"},
		{Type: domain.AgentHookActionRemoveLabel, Value: "wip"},
		{Type: domain.AgentHookActionAddLabel, Value: "reviewed"},
	}}
	ap := newHookAgentProcess(t, "T-2", hooks)
	ap.AgentSessionID = "sess-1"
	r := newLabelOpRecorder(nil)
	s := &Supervisor{IssueBackend: r}

	if got := s.runCompletionHooks(ap, 0); got != 0 {
		t.Fatalf("exit code = %d, want 0", got)
	}
	want := []string{"remove:needs-review", "remove:wip", "add:reviewed"}
	if got := r.seq(); !equalStrings(got, want) {
		t.Fatalf("write order = %v, want %v", got, want)
	}
}

// A stored pipeline that puts a comment after a removal must be REFUSED, not
// quietly reordered — the same treatment an add_label-then-comment pipeline
// gets. This is the path a definition written by an older or newer peer takes.
func TestExecuteCompletionHooks_RefusesACommentAfterARemoval(t *testing.T) {
	stored := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionRemoveLabel, Value: "needs-review"},
		{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
	}}
	r := newLabelOpRecorder(nil)
	s := &Supervisor{IssueBackend: r}
	ap := newHookAgentProcess(t, "T-1", stored)

	err := s.executeCompletionHooks(context.Background(), ap, stored, "T-1", "sess-1")
	if err == nil {
		t.Fatal("expected the invalid stored pipeline to be refused")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("error = %v, want it to name the invalid pipeline", err)
	}
	if got := r.seq(); len(got) != 0 {
		t.Fatalf("no write may happen for a refused pipeline, got %v", got)
	}
}

// The regression pin for everyone who is NOT using this action: a pipeline
// without a remove_label must behave exactly as it did before the action
// existed — same writes, same order, and RemoveLabel never called at all.
func TestRunCompletionHooks_PipelineWithoutARemovalIsUnchanged(t *testing.T) {
	sess := removeLabelSession(t)

	ap := newHookAgentProcess(t, "T-1", hookPipeline())
	ap.AgentSessionID = sess.SessionID()
	r := newLabelOpRecorder(nil)
	s := &Supervisor{IssueBackend: r}

	if got := s.runCompletionHooks(ap, 0); got != 0 {
		t.Fatalf("exit code = %d, want the clean exit preserved", got)
	}
	want := []string{"comment", "add:criticized"}
	if got := r.seq(); !equalStrings(got, want) {
		t.Fatalf("write order = %v, want the pre-remove_label behavior %v", got, want)
	}
	for _, op := range r.seq() {
		if strings.HasPrefix(op, "remove:") {
			t.Fatalf("a pipeline with no remove_label must not remove anything, got %v", r.seq())
		}
	}
}
