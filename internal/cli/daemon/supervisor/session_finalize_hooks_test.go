package supervisor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// --- chunking ---------------------------------------------------------------

func TestChunkComment(t *testing.T) {
	t.Run("short input is one chunk", func(t *testing.T) {
		got := chunkComment("short", 100)
		if len(got) != 1 || got[0] != "short" {
			t.Fatalf("chunkComment() = %v, want one chunk", got)
		}
	})

	t.Run("empty input still yields one chunk", func(t *testing.T) {
		if got := chunkComment("", 100); len(got) != 1 {
			t.Fatalf("chunkComment(\"\") = %v, want exactly one chunk", got)
		}
	})

	t.Run("multi-byte runes are never split", func(t *testing.T) {
		// "é" is 2 bytes and "→" is 3, so an odd budget forces the cut to land
		// mid-rune on nearly every boundary.
		s := strings.Repeat("é→", 200)
		for _, budget := range []int{5, 7, 8, 13} {
			chunks := chunkComment(s, budget)
			if joined := strings.Join(chunks, ""); joined != s {
				t.Fatalf("budget %d: chunks do not rejoin to the original", budget)
			}
			for i, c := range chunks {
				if len(c) > budget {
					t.Fatalf("budget %d: chunk %d is %d bytes, over budget", budget, i, len(c))
				}
				if !utf8.ValidString(c) {
					t.Fatalf("budget %d: chunk %d split a rune: %q", budget, i, c)
				}
				if c == "" {
					t.Fatalf("budget %d: chunk %d is empty", budget, i)
				}
			}
		}
	})

	t.Run("chunks stay under the fleet-db comment cap with headers", func(t *testing.T) {
		s := strings.Repeat("ünïcödé ", 4000)
		chunks := chunkComment(s, maxCommentBytes-chunkHeaderReserve)
		if len(chunks) < 2 {
			t.Fatalf("expected the oversized reply to split, got %d chunk(s)", len(chunks))
		}
		for i, c := range chunks {
			withHeader := fmt.Sprintf("[final reply - part %d/%d]\n\n%s", i+1, len(chunks), c)
			if len(withHeader) > maxCommentBytes {
				t.Fatalf("chunk %d with header is %d bytes, over the %d-byte cap",
					i, len(withHeader), maxCommentBytes)
			}
		}
	})
}

// --- eligibility ------------------------------------------------------------

func hookPipeline() *domain.AgentHooks {
	return &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
		{Type: domain.AgentHookActionAddLabel, Value: "criticized"},
	}}
}

// newHookAgentProcess builds an AgentProcess whose lock file names taskID, so
// taskIDForFinalize resolves without a live control plane.
func newHookAgentProcess(t *testing.T, taskID string, hooks *domain.AgentHooks) *AgentProcess {
	t.Helper()
	worktree := t.TempDir()
	if taskID != "" {
		writeLockFile(t, worktree, &cli.LockInfo{TaskID: taskID})
	}
	return &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "critic", Role: "critic", Hooks: hooks},
		WorktreePath: worktree,
	}
}

func TestCompletionHookTarget_Eligibility(t *testing.T) {
	noWork := &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome)}

	tests := []struct {
		name       string
		hooks      *domain.AgentHooks
		exitCode   int
		lastErr    *agenterr.AgentError
		stopReason StopReason
		taskID     string
		nilBackend bool
		wantOK     bool
	}{
		{name: "clean task-bearing exit", hooks: hookPipeline(), taskID: "T-1", wantOK: true},
		{name: "no hooks configured", hooks: nil, taskID: "T-1"},
		{name: "empty pipeline", hooks: &domain.AgentHooks{}, taskID: "T-1"},
		{name: "nonzero exit", hooks: hookPipeline(), exitCode: 1, taskID: "T-1"},
		{name: "classified error", hooks: hookPipeline(), lastErr: &agenterr.AgentError{}, taskID: "T-1"},
		{name: "no-work idle exit", hooks: hookPipeline(), lastErr: noWork, taskID: "T-1"},
		{name: "no task claimed", hooks: hookPipeline(), taskID: ""},
		{name: "no issue backend", hooks: hookPipeline(), taskID: "T-1", nilBackend: true},
		{name: "shutdown", hooks: hookPipeline(), stopReason: StopReasonShutdown, taskID: "T-1"},
		{name: "yielded", hooks: hookPipeline(), stopReason: StopReasonYielded, taskID: "T-1"},
		{name: "watchdog", hooks: hookPipeline(), stopReason: StopReasonWatchdog, taskID: "T-1"},
		{name: "manual stop", hooks: hookPipeline(), stopReason: StopReasonManualStop, taskID: "T-1"},
		{name: "config removed", hooks: hookPipeline(), stopReason: StopReasonConfigRemoved, taskID: "T-1"},
		{name: "backend unavailable", hooks: hookPipeline(), stopReason: StopReasonBackendUnavailable, taskID: "T-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ap := newHookAgentProcess(t, tt.taskID, tt.hooks)
			ap.LastError = tt.lastErr
			ap.StopReason = tt.stopReason

			s := &Supervisor{IssueBackend: clitest.NewMockIssueBackend()}
			if tt.nilBackend {
				s.IssueBackend = nil
			}

			gotHooks, gotTask, ok := s.completionHookTarget(ap, tt.exitCode)
			if ok != tt.wantOK {
				t.Fatalf("eligible = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if gotTask != tt.taskID {
				t.Errorf("taskID = %q, want %q", gotTask, tt.taskID)
			}
			if gotHooks.IsEmpty() {
				t.Error("eligible run returned an empty pipeline")
			}
		})
	}
}

// --- ordered execution ------------------------------------------------------

// recordingBackend records the ordered sequence of hook writes so a test can
// assert that the comment genuinely lands before the label.
type recordingBackend struct {
	*clitest.MockIssueBackend
	mu       sync.Mutex
	sequence []string
	comments []string
}

func newRecordingBackend(commentErr, labelErr error) *recordingBackend {
	rb := &recordingBackend{MockIssueBackend: clitest.NewMockIssueBackend()}
	rb.AddCommentFn = func(_ context.Context, p backend.CommentAddParams) (*backend.CommentData, error) {
		rb.mu.Lock()
		rb.sequence = append(rb.sequence, "comment")
		rb.comments = append(rb.comments, p.Text)
		rb.mu.Unlock()
		if commentErr != nil {
			return nil, commentErr
		}
		return &backend.CommentData{IssueID: p.IssueID, Text: p.Text}, nil
	}
	rb.AddLabelFn = func(_ context.Context, _ string, label string) error {
		rb.mu.Lock()
		rb.sequence = append(rb.sequence, "label:"+label)
		rb.mu.Unlock()
		return labelErr
	}
	return rb
}

func (r *recordingBackend) seq() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sequence...)
}

// runHookActions exercises the ordered-write loop with a pre-supplied reply,
// bypassing the transcript read (covered separately by the extraction tests).
func runHookActions(t *testing.T, s *Supervisor, ap *AgentProcess, hooks *domain.AgentHooks, reply string) error {
	t.Helper()
	if err := hooks.Validate(); err != nil {
		return fmt.Errorf("stored on_complete pipeline is invalid: %w", err)
	}
	for i, action := range hooks.OnComplete {
		var err error
		switch action.Type {
		case domain.AgentHookActionComment:
			err = s.postFinalReplyComment(context.Background(), ap, "T-1", reply)
		case domain.AgentHookActionAddLabel:
			err = s.IssueBackend.AddLabel(context.Background(), "T-1", action.Value)
		}
		if err != nil {
			return fmt.Errorf("on_complete[%d] (%s): %w", i, action.Type, err)
		}
	}
	return nil
}

func TestExecuteCompletionHooks_Ordering(t *testing.T) {
	t.Run("comment lands before the label", func(t *testing.T) {
		rb := newRecordingBackend(nil, nil)
		s := &Supervisor{IssueBackend: rb}
		ap := newHookAgentProcess(t, "T-1", hookPipeline())

		if err := runHookActions(t, s, ap, hookPipeline(), "the review"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"comment", "label:criticized"}
		if got := rb.seq(); !equalStrings(got, want) {
			t.Fatalf("write order = %v, want %v", got, want)
		}
	})

	t.Run("a failing comment means the label is never attempted", func(t *testing.T) {
		rb := newRecordingBackend(errors.New("comment boom"), nil)
		s := &Supervisor{IssueBackend: rb}
		ap := newHookAgentProcess(t, "T-1", hookPipeline())

		err := runHookActions(t, s, ap, hookPipeline(), "the review")
		if err == nil {
			t.Fatal("expected an error")
		}
		for _, call := range rb.seq() {
			if strings.HasPrefix(call, "label:") {
				t.Fatalf("label was stamped despite a failed comment: %v", rb.seq())
			}
		}
	})

	t.Run("a failing label leaves the comment posted", func(t *testing.T) {
		rb := newRecordingBackend(nil, errors.New("label boom"))
		s := &Supervisor{IssueBackend: rb}
		ap := newHookAgentProcess(t, "T-1", hookPipeline())

		if err := runHookActions(t, s, ap, hookPipeline(), "the review"); err == nil {
			t.Fatal("expected an error")
		}
		want := []string{"comment", "label:criticized"}
		if got := rb.seq(); !equalStrings(got, want) {
			t.Fatalf("write order = %v, want %v", got, want)
		}
	})

	t.Run("every chunk of an oversized reply posts before the label", func(t *testing.T) {
		rb := newRecordingBackend(nil, nil)
		s := &Supervisor{IssueBackend: rb}
		ap := newHookAgentProcess(t, "T-1", hookPipeline())

		reply := strings.Repeat("a", maxCommentBytes*2)
		if err := runHookActions(t, s, ap, hookPipeline(), reply); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		seq := rb.seq()
		if len(seq) < 4 {
			t.Fatalf("expected several comment chunks plus a label, got %v", seq)
		}
		if seq[len(seq)-1] != "label:criticized" {
			t.Fatalf("label must be last, got %v", seq)
		}
		for _, call := range seq[:len(seq)-1] {
			if call != "comment" {
				t.Fatalf("expected only comments before the label, got %v", seq)
			}
		}
		rb.mu.Lock()
		defer rb.mu.Unlock()
		for i, c := range rb.comments {
			if !strings.HasPrefix(c, fmt.Sprintf("[final reply - part %d/%d]", i+1, len(rb.comments))) {
				t.Fatalf("chunk %d is missing its ordered header: %.40q", i, c)
			}
		}
	})

	t.Run("labels-only pipeline needs no reply", func(t *testing.T) {
		hooks := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
			{Type: domain.AgentHookActionAddLabel, Value: "done"},
		}}
		if completionHooksNeedReply(hooks) {
			t.Fatal("a labels-only pipeline must not require a transcript read")
		}
		if !completionHooksNeedReply(hookPipeline()) {
			t.Fatal("a pipeline with a comment action must require a reply")
		}
	})
}

func TestExecuteCompletionHooks_RefusesInvalidStoredOrder(t *testing.T) {
	// A definition written by an older or newer peer could violate the
	// write-before-stamp order. It must be refused, not reordered or skipped.
	stored := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionAddLabel, Value: "criticized"},
		{Type: domain.AgentHookActionComment, Source: domain.AgentHookCommentSourceFinalReply},
	}}
	rb := newRecordingBackend(nil, nil)
	s := &Supervisor{IssueBackend: rb}
	ap := newHookAgentProcess(t, "T-1", stored)

	err := s.executeCompletionHooks(context.Background(), ap, stored, "T-1", "sess-1")
	if err == nil {
		t.Fatal("expected the invalid stored pipeline to be refused")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("error = %v, want it to name the invalid pipeline", err)
	}
	if got := rb.seq(); len(got) != 0 {
		t.Fatalf("no write may happen for a refused pipeline, got %v", got)
	}
}

// --- failure demotion -------------------------------------------------------

func TestMarkCompletionHookFailure(t *testing.T) {
	ap := newHookAgentProcess(t, "T-1", hookPipeline())
	ap.LastExitCode = 0
	ap.LastNoWork = true

	s := &Supervisor{}
	s.markCompletionHookFailure(ap, errors.New("label boom"))

	if ap.LastExitCode != -1 {
		t.Errorf("LastExitCode = %d, want -1", ap.LastExitCode)
	}
	if ap.LastNoWork {
		t.Error("LastNoWork must be cleared by a hook failure")
	}
	if ap.LastError == nil {
		t.Fatal("LastError = nil, want a CompletionHookFailure")
	}
	want := agenterr.OutcomeFromDomain(agenterr.CompletionHookFailureOutcome)
	if ap.LastError.Class != want {
		t.Errorf("LastError.Class = %v, want %v", ap.LastError.Class, want)
	}
	if ap.LastError.ExitCode != -1 {
		t.Errorf("LastError.ExitCode = %d, want -1", ap.LastError.ExitCode)
	}
	if !strings.Contains(ap.LastError.Message, "label boom") {
		t.Errorf("LastError.Message = %q, want it to carry the cause", ap.LastError.Message)
	}
}

func TestRunCompletionHooks_DemotesOnFailureAndPreservesOnSkip(t *testing.T) {
	t.Run("ineligible run is untouched", func(t *testing.T) {
		ap := newHookAgentProcess(t, "T-1", nil) // no hooks configured
		s := &Supervisor{IssueBackend: clitest.NewMockIssueBackend()}

		if got := s.runCompletionHooks(ap, 0); got != 0 {
			t.Errorf("exit code = %d, want 0", got)
		}
		if ap.LastError != nil {
			t.Errorf("LastError = %+v, want nil", ap.LastError)
		}
	})

	t.Run("nonzero exit is passed through unchanged", func(t *testing.T) {
		ap := newHookAgentProcess(t, "T-1", hookPipeline())
		s := &Supervisor{IssueBackend: clitest.NewMockIssueBackend()}

		if got := s.runCompletionHooks(ap, 3); got != 3 {
			t.Errorf("exit code = %d, want 3", got)
		}
	})

	t.Run("hook failure demotes the run to a synthetic failure", func(t *testing.T) {
		// No transcript exists for this session id, so reply extraction fails
		// closed — the same demotion path a failed issue write takes.
		withShortFlushWindow(t)
		ap := newHookAgentProcess(t, "T-1", hookPipeline())
		ap.AgentSessionID = "sess-missing"
		rb := newRecordingBackend(nil, nil)
		s := &Supervisor{IssueBackend: rb}

		if got := s.runCompletionHooks(ap, 0); got != -1 {
			t.Fatalf("exit code = %d, want -1", got)
		}
		if ap.LastError == nil ||
			ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.CompletionHookFailureOutcome) {
			t.Fatalf("LastError = %+v, want CompletionHookFailure", ap.LastError)
		}
		if got := rb.seq(); len(got) != 0 {
			t.Fatalf("no label may be stamped when the artifact is missing, got %v", got)
		}
	})
}

func TestFinalAssistantReply_RequiresSessionID(t *testing.T) {
	s := &Supervisor{}
	if _, err := s.finalAssistantReply(context.Background(), ""); err == nil {
		t.Fatal("expected an error for a missing session id")
	}
}

// withShortFlushWindow shrinks the fail-closed transcript wait so a test that
// deliberately has no transcript does not burn the full production window.
func withShortFlushWindow(t *testing.T) {
	t.Helper()
	prev := transcriptFlushWindow
	transcriptFlushWindow = 10 * time.Millisecond
	t.Cleanup(func() { transcriptFlushWindow = prev })
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
