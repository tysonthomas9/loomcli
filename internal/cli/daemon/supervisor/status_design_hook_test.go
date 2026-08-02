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

// fieldOpRecorder records every hook write in issue order, the task each one
// targeted, and the Update params as they reach the fleet client. The params
// matter as much as the sequence: the whole claim-lock argument for set_status
// rests on WHICH fields go into one Update, so a test that only counted calls
// would pass while the ordering guarantee was gone.
type fieldOpRecorder struct {
	*clitest.MockIssueBackend
	mu       sync.Mutex
	sequence []string
	targets  []string
	updates  []backend.UpdateParams
}

func newFieldOpRecorder(updateErr error) *fieldOpRecorder {
	r := &fieldOpRecorder{MockIssueBackend: clitest.NewMockIssueBackend()}
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
		return nil
	}
	r.UpdateFn = func(_ context.Context, id string, p backend.UpdateParams) error {
		op := "update"
		switch {
		case p.Design != nil:
			op = "design"
		case p.Status != nil:
			op = "status:" + *p.Status
		}
		r.mu.Lock()
		r.updates = append(r.updates, p)
		r.mu.Unlock()
		r.record(op, id)
		return updateErr
	}
	return r
}

func (r *fieldOpRecorder) record(op, taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequence = append(r.sequence, op)
	r.targets = append(r.targets, taskID)
}

func (r *fieldOpRecorder) seq() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sequence...)
}

func (r *fieldOpRecorder) targetsSeen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.targets...)
}

func (r *fieldOpRecorder) lastUpdate(t *testing.T) backend.UpdateParams {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.updates) == 0 {
		t.Fatal("no Update reached the backend")
	}
	return r.updates[len(r.updates)-1]
}

// statusDesignSession lays down a real on-disk transcript so a pipeline whose
// body writes need the run's artifact can run through the production entry point.
func statusDesignSession(t *testing.T) *sessions.Session {
	t.Helper()
	store := hookSessionRuntime(t)
	sess, err := store.CreateSession(sessions.CreateOptions{AgentName: "critic", Backend: backendnames.Codex})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	syncCanonicalTranscript(t, store, sess.SessionID(), canonicalReplyTranscript)
	return sess
}

// TestRunCompletionHooks_WriteDesignAndSetStatusOrdering drives the PRODUCTION
// entry point over the order hooksFromFlags builds: design, comment, removal,
// stamp, status.
//
// The design and the comment are body writes and must land before anything
// stamps; the status goes last because in loom it is the claimability gate —
// opening the task before its hand-off label is on would make it claimable while
// unrouted.
func TestRunCompletionHooks_WriteDesignAndSetStatusOrdering(t *testing.T) {
	sess := statusDesignSession(t)

	hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionWriteDesign, Source: domain.AgentHookCommentSourceFinalReply},
		{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
		{Type: domain.AgentHookActionRemoveLabel, Value: "needs-plan"},
		{Type: domain.AgentHookActionAddLabel, Value: "planned"},
		{Type: domain.AgentHookActionSetStatus, Value: "open"},
	}}
	ap := newHookAgentProcess(t, "T-9", hooks)
	ap.AgentSessionID = sess.SessionID()
	r := newFieldOpRecorder(nil)
	s := &Supervisor{IssueBackend: r}

	if got := s.runCompletionHooks(ap, 0); got != 0 {
		t.Fatalf("exit code = %d, want the clean exit preserved", got)
	}
	if ap.LastError != nil {
		t.Fatalf("LastError = %+v, want nil for a fully applied pipeline", ap.LastError)
	}
	want := []string{"design", "comment", "remove:needs-plan", "add:planned", "status:open"}
	if got := r.seq(); !equalStrings(got, want) {
		t.Fatalf("write order = %v, want %v", got, want)
	}
	// Every write, the design and the status included, targets the task this run
	// owned. A design written to somebody else's task would still look clean.
	for i, target := range r.targetsSeen() {
		if target != "T-9" {
			t.Errorf("write %d (%s) targeted %q, want the owned task T-9", i, r.seq()[i], target)
		}
	}
}

// The design is the run's extracted final reply — the SAME text the comment
// posts, resolved once. A second extraction could disagree with the first about
// what the run's artifact was, and a task whose comment says one thing and whose
// design records another is worse than either alone.
func TestRunCompletionHooks_WriteDesignReusesTheCommentExtraction(t *testing.T) {
	sess := statusDesignSession(t)

	hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionWriteDesign, Source: domain.AgentHookCommentSourceFinalReply},
		{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
	}}
	ap := newHookAgentProcess(t, "T-1", hooks)
	ap.AgentSessionID = sess.SessionID()
	r := newFieldOpRecorder(nil)
	s := &Supervisor{IssueBackend: r}

	if got := s.runCompletionHooks(ap, 0); got != 0 {
		t.Fatalf("exit code = %d, want 0", got)
	}
	design := r.lastUpdate(t)
	if design.Design == nil {
		t.Fatal("Update carried no design field")
	}
	if *design.Design != wantFinalReply {
		t.Errorf("design = %q, want the extracted final reply %q", *design.Design, wantFinalReply)
	}
	// Nothing else rides along: a design write must not silently move the status
	// or the notes.
	if design.Status != nil || design.Notes != nil {
		t.Errorf("design Update = %+v, want only the design field set", design)
	}
}

// A design write is a real write, so its failure has to demote the run the way a
// failed stamp does: the pipeline stops at the first error and the certifying
// label is never attempted.
func TestRunCompletionHooks_WriteDesignFailureDemotesTheRun(t *testing.T) {
	sess := statusDesignSession(t)

	hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionWriteDesign, Source: domain.AgentHookCommentSourceFinalReply},
		{Type: domain.AgentHookActionAddLabel, Value: "planned"},
	}}
	ap := newHookAgentProcess(t, "T-1", hooks)
	ap.AgentSessionID = sess.SessionID()
	r := newFieldOpRecorder(errors.New("design boom"))
	s := &Supervisor{IssueBackend: r}

	if got := s.runCompletionHooks(ap, 0); got != -1 {
		t.Fatalf("exit code = %d, want -1 for a failed design write", got)
	}
	if ap.LastError == nil ||
		ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.CompletionHookFailureOutcome) {
		t.Fatalf("LastError = %+v, want CompletionHookFailure", ap.LastError)
	}
	if !strings.Contains(ap.LastError.Message, "write_design") {
		t.Errorf("LastError.Message = %q, want it to name the failed action", ap.LastError.Message)
	}
	if got := r.seq(); !equalStrings(got, []string{"design"}) {
		t.Fatalf("write sequence = %v, want the pipeline to stop at the failed design write", got)
	}
}

// An empty artifact must fail the action, never clear the field. Silently
// wiping a planner's design and reporting a clean run is the failure mode this
// guard exists for.
func TestWriteTaskDesign_RefusesAnEmptyReply(t *testing.T) {
	for _, reply := range []string{"", "   \t\n "} {
		r := newFieldOpRecorder(nil)
		s := &Supervisor{IssueBackend: r}

		err := s.writeTaskDesign(context.Background(), "T-1", reply)
		if err == nil {
			t.Fatalf("writeTaskDesign(%q) = nil, want a refusal", reply)
		}
		if !strings.Contains(err.Error(), "empty design") {
			t.Errorf("error = %v, want it to say an empty design was refused", err)
		}
		if got := r.seq(); len(got) != 0 {
			t.Fatalf("no write may reach the backend for an empty reply, got %v", got)
		}
	}
}

// End to end: a run whose transcript carries no substantive assistant output
// fails closed BEFORE any write, so the design field is left as the planner's
// previous run wrote it rather than being cleared by a run that produced nothing.
func TestRunCompletionHooks_NoReplyLeavesTheDesignUntouched(t *testing.T) {
	withShortFlushWindow(t)
	hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionWriteDesign, Source: domain.AgentHookCommentSourceFinalReply},
	}}
	ap := newHookAgentProcess(t, "T-1", hooks)
	ap.AgentSessionID = "sess-missing"
	r := newFieldOpRecorder(nil)
	s := &Supervisor{IssueBackend: r}

	if got := s.runCompletionHooks(ap, 0); got != -1 {
		t.Fatalf("exit code = %d, want -1 when the artifact is missing", got)
	}
	if got := r.seq(); len(got) != 0 {
		t.Fatalf("no design may be written without an artifact, got %v", got)
	}
}

// A pipeline that only sets a status needs no transcript at all: neither action
// here draws on the run's artifact, so demanding one would fail runs that have
// nothing to publish.
func TestCompletionHooksNeedReply_BodyWritesOnly(t *testing.T) {
	tests := []struct {
		name    string
		actions []domain.AgentHookAction
		want    bool
	}{
		{
			name:    "status only",
			actions: []domain.AgentHookAction{{Type: domain.AgentHookActionSetStatus, Value: "review"}},
		},
		{
			name: "write_design needs the artifact",
			actions: []domain.AgentHookAction{
				{Type: domain.AgentHookActionWriteDesign, Source: domain.AgentHookCommentSourceFinalReply},
			},
			want: true,
		},
		{
			name: "comment needs the artifact",
			actions: []domain.AgentHookAction{
				{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := completionHooksNeedReply(&domain.AgentHooks{OnComplete: tt.actions}); got != tt.want {
				t.Errorf("completionHooksNeedReply() = %v, want %v", got, tt.want)
			}
		})
	}
}

// set_status issues ONE Update carrying only the status.
//
// Assignee is deliberately absent: review/blocked transitions out of in_progress
// release the claim lock AS current.Assignee inside the fleet client
// (transitionToBlockedOrReview, LOOM-1), and shouldAssignBeforeStatus exists to
// stop an assign from running first and erasing the identity that release needs.
// Passing no assignee means the question never arises — the lock holder is still
// on the issue when the release runs. An assignee appearing here would silently
// reintroduce the leaked-lock bug.
func TestRunCompletionHooks_SetStatusWritesOnlyTheStatus(t *testing.T) {
	for _, status := range []string{"review", "open", "deferred"} {
		t.Run(status, func(t *testing.T) {
			hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
				{Type: domain.AgentHookActionSetStatus, Value: status},
			}}
			ap := newHookAgentProcess(t, "T-4", hooks)
			ap.AgentSessionID = "sess-1"
			r := newFieldOpRecorder(nil)
			s := &Supervisor{IssueBackend: r}

			if got := s.runCompletionHooks(ap, 0); got != 0 {
				t.Fatalf("exit code = %d, want 0", got)
			}
			if got := r.seq(); !equalStrings(got, []string{"status:" + status}) {
				t.Fatalf("write sequence = %v, want a single status write", got)
			}
			if got := r.targetsSeen(); !equalStrings(got, []string{"T-4"}) {
				t.Fatalf("status write targeted %v, want the owned task T-4", got)
			}
			p := r.lastUpdate(t)
			if p.Status == nil || *p.Status != status {
				t.Fatalf("Update status = %v, want %q", p.Status, status)
			}
			if p.Assignee != nil {
				t.Errorf("Update carried assignee %q: the claim-lock release inside the fleet "+
					"client needs current.Assignee intact (LOOM-1)", *p.Assignee)
			}
			if p.Notes != nil {
				t.Errorf("Update carried notes %q for a non-blocked status, want none", *p.Notes)
			}
		})
	}
}

// A blocked transition carries its reason in the SAME Update, so the fleet
// client's decomposition puts the notes PATCH ahead of the status transition and
// the card is never observably blocked without the note explaining it. The
// prefix matches what `data update`'s enforceBlockReason tells operators to
// write, because the board's needs-attention state is blocked-with-notes.
func TestRunCompletionHooks_SetStatusBlockedCarriesTheReason(t *testing.T) {
	hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionSetStatus, Value: "blocked", Reason: "upstream API decision pending"},
	}}
	ap := newHookAgentProcess(t, "T-5", hooks)
	ap.AgentSessionID = "sess-1"
	r := newFieldOpRecorder(nil)
	s := &Supervisor{IssueBackend: r}

	if got := s.runCompletionHooks(ap, 0); got != 0 {
		t.Fatalf("exit code = %d, want 0", got)
	}
	// One Update, not a status write plus a follow-up that could fail after the
	// status has already landed.
	if got := r.seq(); !equalStrings(got, []string{"status:blocked"}) {
		t.Fatalf("write sequence = %v, want the reason to ride along in the status write", got)
	}
	p := r.lastUpdate(t)
	if p.Notes == nil {
		t.Fatal("blocked Update carried no notes: a blocked card with no signal sits until a human reviews it")
	}
	if *p.Notes != "BLOCKED: upstream API decision pending" {
		t.Errorf("notes = %q, want the BLOCKED-prefixed reason enforceBlockReason asks for", *p.Notes)
	}
}

// A failed status write demotes the run like any other hook write: the pipeline
// stops and the task is reopened for a bounded retry.
func TestRunCompletionHooks_SetStatusFailureDemotesTheRun(t *testing.T) {
	hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionSetStatus, Value: "review"},
		{Type: domain.AgentHookActionAddLabel, Value: "reviewed"},
	}}
	ap := newHookAgentProcess(t, "T-1", hooks)
	ap.AgentSessionID = "sess-1"
	r := newFieldOpRecorder(errors.New("status boom"))
	s := &Supervisor{IssueBackend: r}

	if got := s.runCompletionHooks(ap, 0); got != -1 {
		t.Fatalf("exit code = %d, want -1 for a failed status write", got)
	}
	if ap.LastError == nil ||
		ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.CompletionHookFailureOutcome) {
		t.Fatalf("LastError = %+v, want CompletionHookFailure", ap.LastError)
	}
	if !strings.Contains(ap.LastError.Message, "set_status") {
		t.Errorf("LastError.Message = %q, want it to name the failed action", ap.LastError.Message)
	}
	if got := r.seq(); !equalStrings(got, []string{"status:review"}) {
		t.Fatalf("write sequence = %v, want the pipeline to stop at the failed status write", got)
	}
}

// A stored pipeline that puts a body write after a status must be REFUSED, not
// quietly reordered — the same treatment an add_label-then-comment pipeline
// gets. This is the path a definition written by an older or newer peer takes.
func TestExecuteCompletionHooks_RefusesABodyWriteAfterASetStatus(t *testing.T) {
	for _, tt := range []struct {
		name string
		body domain.AgentHookAction
	}{
		{name: "comment", body: domain.AgentHookAction{
			Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply,
		}},
		{name: "write_design", body: domain.AgentHookAction{
			Type: domain.AgentHookActionWriteDesign, Source: domain.AgentHookCommentSourceFinalReply,
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stored := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
				{Type: domain.AgentHookActionSetStatus, Value: "review"},
				tt.body,
			}}
			r := newFieldOpRecorder(nil)
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
		})
	}
}

// A stored set_status the server would refuse must not execute either: the
// supervisor's defensive re-validation runs the same PATCH contract, so a
// pipeline stored by a peer that does not know the rule fails before it writes
// rather than 400ing mid-pipeline with earlier writes already applied.
func TestExecuteCompletionHooks_RefusesAnUnsettableStoredStatus(t *testing.T) {
	for _, tt := range []struct {
		status  string
		wantErr string
	}{
		{status: "closed", wantErr: "close endpoint"},
		{status: "in_progress", wantErr: "claim endpoint"},
		{status: "blocked", wantErr: "requires a non-blank reason"},
	} {
		t.Run(tt.status, func(t *testing.T) {
			stored := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
				{Type: domain.AgentHookActionAddLabel, Value: "reviewed"},
				{Type: domain.AgentHookActionSetStatus, Value: tt.status},
			}}
			r := newFieldOpRecorder(nil)
			s := &Supervisor{IssueBackend: r}
			ap := newHookAgentProcess(t, "T-1", stored)

			err := s.executeCompletionHooks(context.Background(), ap, stored, "T-1", "sess-1")
			if err == nil {
				t.Fatalf("expected %q to be refused", tt.status)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
			}
			// The label ahead of it must not land either: the refusal happens
			// before any write, so a rejected pipeline is all-or-nothing.
			if got := r.seq(); len(got) != 0 {
				t.Fatalf("no write may happen for a refused pipeline, got %v", got)
			}
		})
	}
}

// The regression pin for everyone who is NOT using these actions: a pipeline
// with neither must behave exactly as it did before they existed — same writes,
// same order, and Update never called at all, so no status or design is touched.
func TestRunCompletionHooks_PipelineWithNeitherActionIsUnchanged(t *testing.T) {
	sess := statusDesignSession(t)

	ap := newHookAgentProcess(t, "T-1", hookPipeline())
	ap.AgentSessionID = sess.SessionID()
	r := newFieldOpRecorder(nil)
	s := &Supervisor{IssueBackend: r}

	if got := s.runCompletionHooks(ap, 0); got != 0 {
		t.Fatalf("exit code = %d, want the clean exit preserved", got)
	}
	if ap.LastError != nil {
		t.Fatalf("LastError = %+v, want nil", ap.LastError)
	}
	want := []string{"comment", "add:criticized"}
	if got := r.seq(); !equalStrings(got, want) {
		t.Fatalf("write order = %v, want the pre-set_status/write_design behavior %v", got, want)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.updates) != 0 {
		t.Fatalf("a pipeline with neither action must not issue an Update, got %+v", r.updates)
	}
}
