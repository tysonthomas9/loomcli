package supervisor

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// TestExtractLeafUsage_ParsesResultEntryUsage proves the supervisor recovers the
// TS leaf's token/cost usage from the terminal `result` entry's `output` field —
// the channel that fixes the daemon session-metadata tokens=0 finding (the reaped
// worker's collector-aware finalize never runs, so the supervisor sources usage here).
func TestExtractLeafUsage_ParsesResultEntryUsage(t *testing.T) {
	data := []byte(strings.Join([]string{
		`{"role":"system","type":"session_meta","text":"local-cli-codex session"}`,
		`{"role":"assistant","type":"text","text":"done"}`,
		`{"role":"system","type":"result","text":"completed","output":"{\"input_tokens\":8000,\"output_tokens\":300,\"cache_read_tokens\":12,\"cache_write_tokens\":7,\"cost_usd\":0.42}"}`,
	}, "\n") + "\n")

	u := extractLeafUsage(data)
	if u.InputTokens != 8000 || u.OutputTokens != 300 || u.CacheReadTokens != 12 || u.CacheWriteTokens != 7 {
		t.Fatalf("token mismatch: %+v", u)
	}
	if u.CostUSD != 0.42 {
		t.Errorf("cost = %v, want 0.42", u.CostUSD)
	}
}

// TestExtractLeafUsage_RawStreamHasNoUsage proves unrelated raw backend stream
// lines (no result / token_count / turn.completed) still yield zero usage.
func TestExtractLeafUsage_RawStreamHasNoUsage(t *testing.T) {
	data := []byte(`{"type":"response_item","payload":{"role":"assistant"}}` + "\n")
	if u := extractLeafUsage(data); u != (leafUsage{}) {
		t.Errorf("raw stream must yield zero usage, got %+v", u)
	}
}

func TestExtractLeafUsage_CodexTokenCount(t *testing.T) {
	data := []byte(`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":4200,"output_tokens":880,"cached_input_tokens":100}}}}` + "\n")
	u := extractLeafUsage(data)
	if u.InputTokens != 4200 || u.OutputTokens != 880 || u.CacheReadTokens != 100 {
		t.Fatalf("got %+v", u)
	}
}

// TestExtractLeafUsage_IgnoresEstimatedCost pins that only the provider-reported
// cost_usd is carried through; a leaf's estimated_cost_usd is never persisted.
func TestExtractLeafUsage_IgnoresEstimatedCost(t *testing.T) {
	data := []byte(`{"role":"system","type":"result","output":"{\"input_tokens\":10,\"estimated_cost_usd\":2.5}"}` + "\n")
	if u := extractLeafUsage(data); u.CostUSD != 0 {
		t.Errorf("CostUSD = %v, want 0 (no estimate fallback)", u.CostUSD)
	}
}

// TestExecuteCompletionHooks_TerminalTaskSkipsRemainingActionsWithoutDemotion is
// the test that closes the re-dispatch loop. When the design write hits the
// server's "issue is closed" conflict, the row is terminal by decision and no
// retry can ever land the write — so the pipeline must stop, return nil, and
// leave the run its factual exit code. Demoting it to -1 reopens the task and
// re-dispatches it into the same wall, round after round (PUPPET-618).
func TestExecuteCompletionHooks_TerminalTaskSkipsRemainingActionsWithoutDemotion(t *testing.T) {
	sess := statusDesignSession(t)

	hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionWriteDesign, Source: domain.AgentHookCommentSourceFinalReply},
		{Type: domain.AgentHookActionAddLabel, Value: "ready-to-implement"},
		{Type: domain.AgentHookActionSetStatus, Value: "open"},
	}}
	ap := newHookAgentProcess(t, "T-618", hooks)
	ap.AgentSessionID = sess.SessionID()
	r := newFieldOpRecorder(backend.ErrConflict("Update", "issue is closed"))
	s := &Supervisor{IssueBackend: r}

	if got := s.runCompletionHooks(ap, 0); got != 0 {
		t.Fatalf("exit code = %d, want the factual exit 0 preserved (not a demotion)", got)
	}
	if ap.LastError != nil {
		t.Fatalf("LastError = %+v, want nil: a terminal row is a decision, not a hook failure", ap.LastError)
	}
	// The design write was attempted and refused; nothing after it ran. The
	// remaining actions are skipped, not retried — they would fail for the same
	// unfixable reason.
	if got := r.seq(); !equalStrings(got, []string{"design"}) {
		t.Fatalf("write sequence = %v, want the pipeline to stop at the terminal-row conflict", got)
	}
}

// The carve-out stays narrow: a conflict that is NOT about a terminal row is an
// ordinary hook failure and must still demote the run so the task is retried.
func TestExecuteCompletionHooks_OtherConflictStillFails(t *testing.T) {
	sess := statusDesignSession(t)

	hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionWriteDesign, Source: domain.AgentHookCommentSourceFinalReply},
		{Type: domain.AgentHookActionAddLabel, Value: "ready-to-implement"},
	}}
	ap := newHookAgentProcess(t, "T-1", hooks)
	ap.AgentSessionID = sess.SessionID()
	r := newFieldOpRecorder(backend.ErrConflict("Update", "claim is held by another session"))
	s := &Supervisor{IssueBackend: r}

	if got := s.runCompletionHooks(ap, 0); got != -1 {
		t.Fatalf("exit code = %d, want -1: a non-terminal conflict still demotes", got)
	}
	if ap.LastError == nil ||
		ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.CompletionHookFailureOutcome) {
		t.Fatalf("LastError = %+v, want CompletionHookFailure", ap.LastError)
	}
	if got := r.seq(); !equalStrings(got, []string{"design"}) {
		t.Fatalf("write sequence = %v, want the pipeline to stop at the failed design write", got)
	}
}

// TestReopenForNextStage_NeverWritesToAClosedTask pins the two guards that keep
// the re-arm path from writing to a terminal row: advanceReviewCycle refuses to
// advance a closed task at all, and reopenForNextStage skips the Update when the
// task is already `open`.
func TestReopenForNextStage_NeverWritesToAClosedTask(t *testing.T) {
	r := newFieldOpRecorder(nil)
	r.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Status: "closed"}}, nil
	}
	s := &Supervisor{IssueBackend: r}
	cycle := &domain.AgentHookCycle{
		RearmLabel: "needs-review",
		ShipLabel:  "ready-to-implement",
		Threshold:  2,
	}

	if err := s.advanceReviewCycle(context.Background(), "T-closed", cycle); err != nil {
		t.Fatalf("advanceReviewCycle on a closed task = %v, want nil (a decision, not a failure)", err)
	}
	if got := r.seq(); len(got) != 0 {
		t.Fatalf("no write may reach a closed task, got %v", got)
	}

	// And the direct call: an already-open task needs no Update either.
	if err := s.reopenForNextStage(context.Background(), "T-open", "open"); err != nil {
		t.Fatalf("reopenForNextStage(open) = %v, want nil", err)
	}
	if got := r.seq(); len(got) != 0 {
		t.Fatalf("reopenForNextStage wrote %v for an already-open task, want no write", got)
	}
}
