package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// TestWrapIssueBackendWithTracing_Smoke exercises every wrapped method so
// the span-decorator paths are reached. Uses MockIssueBackend from the
// existing test scaffolding; errors are ignored because the wrapper's job
// is unchanged regardless of the inner backend's outcome.
func TestWrapIssueBackendWithTracing_Smoke(t *testing.T) {
	inner := NewMockIssueBackend()
	wrapped := wrapIssueBackendWithTracing(inner)
	if wrapped == nil {
		t.Fatal("wrapIssueBackendWithTracing returned nil for non-nil input")
	}

	if name := wrapped.BackendName(); name == "" {
		t.Error("BackendName empty")
	}

	ctx := context.Background()
	_, _ = wrapped.Get(ctx, "id-1")
	_, _ = wrapped.List(ctx, backend.ListOpts{Limit: 10})
	_, _ = wrapped.Ready(ctx, backend.ReadyOpts{Limit: 10})
	_, _ = wrapped.Blocked(ctx, backend.BlockedOpts{Limit: 10})
	stats := wrapped.(workitems.StatsQueries)
	_, _ = stats.Stats(ctx)
	search := wrapped.(workitems.SearchQueries)
	_, _ = search.Search(ctx, workitems.SearchQuery{Query: "query", Limit: 10})
	_, _ = wrapped.Create(ctx, backend.CreateParams{Title: "t"})
	_ = wrapped.Update(ctx, "id-1", backend.UpdateParams{})
	_ = wrapped.ClaimIssue(ctx, "id-1", time.Minute)
	_, _ = wrapped.Close(ctx, "id-1", backend.CloseParams{})
	_ = wrapped.Reopen(ctx, "id-1", backend.ReopenParams{})
	_ = wrapped.Delete(ctx, backend.DeleteParams{IDs: []string{"id-1"}})
	_ = wrapped.AddDependency(ctx, backend.DepAddParams{FromID: "a", ToID: "b"})
	_ = wrapped.RemoveDependency(ctx, backend.DepRemoveParams{FromID: "a", ToID: "b"})
	_, _ = wrapped.ListComments(ctx, "id-1")
	_, _ = wrapped.AddComment(ctx, backend.CommentAddParams{IssueID: "id-1", Text: "hi"})
	_, _ = wrapped.ListEvents(ctx, "id-1", 10)
}

func TestWrapIssueBackendWithTracing_Nil(t *testing.T) {
	if got := wrapIssueBackendWithTracing(nil); got != nil {
		t.Errorf("wrapIssueBackendWithTracing(nil) = %v, want nil", got)
	}
}

// stubAgentInvoker satisfies AgentInvoker with no-op methods so the
// tracing decorator can be exercised without a real LLM backend.
type stubAgentInvoker struct{ err error }

func (s *stubAgentInvoker) InvokeInteractive(string, string, string) error {
	return s.err
}
func (s *stubAgentInvoker) InvokeNonInteractive(string, string, string, <-chan struct{}, *usage.Collector) error {
	return s.err
}

func TestWrapAgentInvokerWithTracing_Smoke(t *testing.T) {
	wrapped := wrapAgentInvokerWithTracing(t.Context(), &stubAgentInvoker{})
	if wrapped == nil {
		t.Fatal("wrapAgentInvokerWithTracing returned nil for non-nil input")
	}
	if err := wrapped.InvokeInteractive("/tmp", "prompt", "agent"); err != nil {
		t.Errorf("InvokeInteractive: %v", err)
	}
	if err := wrapped.InvokeNonInteractive("/tmp", "prompt", "agent", nil, nil); err != nil {
		t.Errorf("InvokeNonInteractive: %v", err)
	}
	// Error path: ensure span error recording is exercised.
	failing := wrapAgentInvokerWithTracing(t.Context(), &stubAgentInvoker{err: errors.New("invoke failed")})
	_ = failing.InvokeInteractive("/tmp", "", "")
	_ = failing.InvokeNonInteractive("/tmp", "", "", nil, nil)
}

func TestWrapAgentInvokerWithTracing_Nil(t *testing.T) {
	if got := wrapAgentInvokerWithTracing(t.Context(), nil); got != nil {
		t.Errorf("wrapAgentInvokerWithTracing(t.Context(), nil) = %v, want nil", got)
	}
}
