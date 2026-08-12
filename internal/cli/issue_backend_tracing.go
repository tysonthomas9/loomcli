package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/observability/tracing"
)

// issueBackendTracerName is the instrumentation library name reported on
// every IssueBackend service span. Stable so dashboards filtering on it
// don't break.
const issueBackendTracerName = "github.com/tysonthomas9/loomcli/internal/cli/backend"

// tracedIssueBackend wraps a backend.IssueBackend and emits one span per
// method call. Spans nest under whatever context the caller passes (the
// CLI root span, an HTTP server span in loom-serve, etc.), giving
// per-method granularity for time spent in the data layer vs other work.
//
// Span name pattern: `service.IssueBackend.<Method>`. This satisfies the
// allowlist regex `^service\.[A-Z][a-zA-Z]+\.[A-Z][a-zA-Z]+$` from
// internal/observability/tracing/cardinality_test.go.
//
// Per the trace contract §6 (PII / redaction), this decorator NEVER
// records issue titles, comment bodies, prompts, or any other free-form
// user-supplied string as a span attribute. Only IDs, names, counts, and
// boolean/numeric flags are captured.
type tracedIssueBackend struct {
	inner       backend.IssueBackend
	backendAttr attribute.KeyValue // cached at construction; backend name is stable
}

var _ workitems.StatsQueries = (*tracedIssueBackend)(nil)
var _ workitems.BlockedQueries = (*tracedIssueBackend)(nil)
var _ workitems.ReadyQueries = (*tracedIssueBackend)(nil)
var _ workitems.EventQueries = (*tracedIssueBackend)(nil)
var _ workitems.CommentQueries = (*tracedIssueBackend)(nil)
var _ workitems.CommentCommands = (*tracedIssueBackend)(nil)
var _ workitems.DependencyCommands = (*tracedIssueBackend)(nil)

// wrapIssueBackendWithTracing returns a tracing-decorated IssueBackend.
// nil-safe: passing nil returns nil so callers can wrap unconditionally.
func wrapIssueBackendWithTracing(inner backend.IssueBackend) backend.IssueBackend {
	if inner == nil {
		return nil
	}
	return &tracedIssueBackend{
		inner:       inner,
		backendAttr: attribute.String("loom.backend", inner.BackendName()),
	}
}

// startSpan starts a service span for the given IssueBackend method.
// Recording-gated: skips attribute construction when the global tracer
// is no-op (the common disabled path).
func (t *tracedIssueBackend) startSpan(ctx context.Context, method string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return t.startServiceSpan(ctx, "IssueBackend", method, attrs...)
}

func (t *tracedIssueBackend) startServiceSpan(ctx context.Context, service, method string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	tracer := tracing.Tracer(issueBackendTracerName)
	ctx, span := tracer.Start(ctx, "service."+service+"."+method)
	if span.IsRecording() {
		span.SetAttributes(t.backendAttr)
		if len(attrs) > 0 {
			span.SetAttributes(attrs...)
		}
	}
	return ctx, span
}

// endSpan finalizes a span, recording the error per trace contract §7:
// context.Canceled is left status-unset; other errors get RecordError +
// SetStatus(Error, "<short reason>"). The reason is a low-cardinality
// category derived from the BackendError ErrorKind when possible.
func endSpan(span trace.Span, err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		span.RecordError(err)
		span.SetStatus(codes.Error, backendErrorReason(err))
	}
	span.End()
}

// backendErrorReason classifies an error into a short, low-cardinality
// status description. Uses BackendError ErrorKind when present so spans
// from different request paths share the same status set.
func backendErrorReason(err error) string {
	var be *backend.BackendError
	if errors.As(err, &be) {
		switch be.Kind {
		case backend.KindNotFound:
			return "not_found"
		case backend.KindConflict:
			return "conflict"
		case backend.KindValidation:
			return "validation"
		case backend.KindUnavailable:
			return "unavailable"
		case backend.KindNotImplemented:
			return "not_implemented"
		}
	}
	return "unknown"
}

// --- Query operations ---

func (t *tracedIssueBackend) Get(ctx context.Context, id string) (*backend.IssueDetailData, error) {
	ctx, span := t.startSpan(ctx, "Get",
		attribute.String("loom.task_id", id),
	)
	out, err := t.inner.Get(ctx, id)
	endSpan(span, err)
	return out, err
}

func (t *tracedIssueBackend) List(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	ctx, span := t.startSpan(ctx, "List")
	out, err := t.inner.List(ctx, opts)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	endSpan(span, err)
	return out, err
}

func (t *tracedIssueBackend) Ready(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	ctx, span := t.startServiceSpan(ctx, "WorkItems", "Ready")
	ready, ok := t.inner.(workitems.ReadyQueries)
	if !ok {
		endSpan(span, workitems.ErrUnavailable)
		return nil, workitems.ErrUnavailable
	}
	out, err := ready.Ready(ctx, query)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	endSpan(span, err)
	return out, err
}

func (t *tracedIssueBackend) Blocked(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	ctx, span := t.startServiceSpan(ctx, "WorkItems", "Blocked")
	blocked, ok := t.inner.(workitems.BlockedQueries)
	if !ok {
		endSpan(span, workitems.ErrUnavailable)
		return nil, workitems.ErrUnavailable
	}
	out, err := blocked.Blocked(ctx, query)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	endSpan(span, err)
	return out, err
}

func (t *tracedIssueBackend) Stats(ctx context.Context) (*workitems.Stats, error) {
	ctx, span := t.startServiceSpan(ctx, "WorkItems", "Stats")
	stats, ok := t.inner.(workitems.StatsQueries)
	if !ok {
		endSpan(span, workitems.ErrUnavailable)
		return nil, workitems.ErrUnavailable
	}
	out, err := stats.Stats(ctx)
	endSpan(span, err)
	return out, err
}

func (t *tracedIssueBackend) Search(ctx context.Context, query workitems.SearchQuery) ([]workitems.IssueSummary, error) {
	// NB: per §6, query content is PII-sensitive — only its length is
	// recorded as an attribute, never the raw string.
	ctx, span := t.startServiceSpan(ctx, "WorkItems", "Search",
		attribute.Int("query.bytes", len(query.Query)),
		attribute.Int("limit", query.Limit),
	)
	search, ok := t.inner.(workitems.SearchQueries)
	if !ok {
		endSpan(span, workitems.ErrUnavailable)
		return nil, workitems.ErrUnavailable
	}
	out, err := search.Search(ctx, query)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	endSpan(span, err)
	return out, err
}

// --- Mutation operations ---

func (t *tracedIssueBackend) Create(ctx context.Context, params backend.CreateParams) (*backend.IssueData, error) {
	// NB: only structural attributes — no title, description, or labels
	// content (titles may contain secrets per §6).
	ctx, span := t.startSpan(ctx, "Create")
	out, err := t.inner.Create(ctx, params)
	endSpan(span, err)
	return out, err
}

func (t *tracedIssueBackend) Update(ctx context.Context, id string, params backend.UpdateParams) error {
	ctx, span := t.startSpan(ctx, "Update",
		attribute.String("loom.task_id", id),
	)
	err := t.inner.Update(ctx, id, params)
	endSpan(span, err)
	return err
}

func (t *tracedIssueBackend) ClaimIssue(ctx context.Context, id string, lockTTL time.Duration) error {
	ctx, span := t.startSpan(ctx, "ClaimIssue",
		attribute.String("loom.task_id", id),
		attribute.Int64("lock_ttl_ms", lockTTL.Milliseconds()),
	)
	err := t.inner.ClaimIssue(ctx, id, lockTTL)
	endSpan(span, err)
	return err
}

func (t *tracedIssueBackend) ReleaseIssueLock(ctx context.Context, id, actor string) error {
	ctx, span := t.startSpan(ctx, "ReleaseIssueLock",
		attribute.String("loom.task_id", id),
	)
	err := t.inner.ReleaseIssueLock(ctx, id, actor)
	endSpan(span, err)
	return err
}

// ReleaseIssueAsActor preserves actor-scoped release through the traced
// decorator when the underlying backend supports it.
func (t *tracedIssueBackend) ReleaseIssueAsActor(ctx context.Context, id, actor string) error {
	ctx, span := t.startSpan(ctx, "ReleaseIssueAsActor",
		attribute.String("loom.task_id", id),
	)
	if actorBackend, ok := t.inner.(interface {
		ReleaseIssueAsActor(context.Context, string, string) error
	}); ok {
		err := actorBackend.ReleaseIssueAsActor(ctx, id, actor)
		endSpan(span, err)
		return err
	}
	err := t.inner.ReleaseIssueLock(ctx, id, actor)
	endSpan(span, err)
	return err
}

func (t *tracedIssueBackend) ClaimIssueAsActor(ctx context.Context, id string, lockTTL time.Duration, actor string) error {
	ctx, span := t.startSpan(ctx, "ClaimIssueAsActor",
		attribute.String("loom.task_id", id),
		attribute.Int64("lock_ttl_ms", lockTTL.Milliseconds()),
	)
	if actorBackend, ok := t.inner.(interface {
		ClaimIssueAsActor(context.Context, string, time.Duration, string) error
	}); ok {
		err := actorBackend.ClaimIssueAsActor(ctx, id, lockTTL, actor)
		endSpan(span, err)
		return err
	}
	err := t.inner.ClaimIssue(ctx, id, lockTTL)
	endSpan(span, err)
	return err
}

func (t *tracedIssueBackend) RenewIssueClaimAsActor(ctx context.Context, id string, lockTTL time.Duration, actor string) error {
	ctx, span := t.startSpan(ctx, "RenewIssueClaimAsActor",
		attribute.String("loom.task_id", id),
		attribute.Int64("lock_ttl_ms", lockTTL.Milliseconds()),
	)
	if actorBackend, ok := t.inner.(interface {
		RenewIssueClaimAsActor(context.Context, string, time.Duration, string) error
	}); ok {
		err := actorBackend.RenewIssueClaimAsActor(ctx, id, lockTTL, actor)
		endSpan(span, err)
		return err
	}
	err := fmt.Errorf("renew issue claim: backend does not support renewal-only claims")
	endSpan(span, err)
	return err
}

// ReleaseClaim forwards to the inner backend when it implements
// backend.ClaimReleaser (capability-detected — only fleet-db has an explicit
// claim lock distinct from issue status). Wrapped in a span like the other
// mutation methods. See LOOM-1: this delegation must exist or `loom complete`'s
// release-on-exit path is silently a no-op when tracing is enabled (the
// default for the CLI).
func (t *tracedIssueBackend) ReleaseClaim(ctx context.Context, id, actor string) error {
	ctx, span := t.startSpan(ctx, "ReleaseClaim",
		attribute.String("loom.task_id", id),
	)
	r, ok := t.inner.(backend.ClaimReleaser)
	if !ok {
		endSpan(span, nil)
		return nil
	}
	err := r.ReleaseClaim(ctx, id, actor)
	endSpan(span, err)
	return err
}

func (t *tracedIssueBackend) Close(ctx context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	ctx, span := t.startSpan(ctx, "Close",
		attribute.String("loom.task_id", id),
	)
	out, err := t.inner.Close(ctx, id, params)
	if err == nil && out != nil {
		span.SetAttributes(attribute.Int("unblocked.count", len(out.Unblocked)))
	}
	endSpan(span, err)
	return out, err
}

func (t *tracedIssueBackend) Reopen(ctx context.Context, id string, params backend.ReopenParams) error {
	ctx, span := t.startSpan(ctx, "Reopen",
		attribute.String("loom.task_id", id),
	)
	err := t.inner.Reopen(ctx, id, params)
	endSpan(span, err)
	return err
}

func (t *tracedIssueBackend) Delete(ctx context.Context, params backend.DeleteParams) error {
	ctx, span := t.startSpan(ctx, "Delete",
		attribute.Int("ids.count", len(params.IDs)),
		attribute.Bool("force", params.Force),
	)
	err := t.inner.Delete(ctx, params)
	endSpan(span, err)
	return err
}

// --- Dependency operations ---

func (t *tracedIssueBackend) AddDependency(ctx context.Context, command workitems.AddDependencyCommand) error {
	ctx, span := t.startServiceSpan(ctx, "WorkItems", "AddDependency")
	dependencies, ok := t.inner.(workitems.DependencyCommands)
	if !ok {
		err := backend.ErrUnavailable("AddDependency", "work items dependency commands unavailable", nil)
		endSpan(span, err)
		return err
	}
	err := dependencies.AddDependency(ctx, command)
	endSpan(span, err)
	return err
}

func (t *tracedIssueBackend) RemoveDependency(ctx context.Context, command workitems.RemoveDependencyCommand) error {
	ctx, span := t.startServiceSpan(ctx, "WorkItems", "RemoveDependency")
	dependencies, ok := t.inner.(workitems.DependencyCommands)
	if !ok {
		err := backend.ErrUnavailable("RemoveDependency", "work items dependency commands unavailable", nil)
		endSpan(span, err)
		return err
	}
	err := dependencies.RemoveDependency(ctx, command)
	endSpan(span, err)
	return err
}

// --- Comment operations ---

func (t *tracedIssueBackend) ListComments(ctx context.Context, query workitems.ListCommentsQuery) ([]*workitems.Comment, error) {
	ctx, span := t.startServiceSpan(ctx, "WorkItems", "ListComments",
		attribute.String("loom.task_id", query.IssueID),
	)
	comments, ok := t.inner.(workitems.CommentQueries)
	if !ok {
		err := backend.ErrUnavailable("ListComments", "work items comment queries unavailable", nil)
		endSpan(span, err)
		return nil, err
	}
	out, err := comments.ListComments(ctx, query)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	endSpan(span, err)
	return out, err
}

func (t *tracedIssueBackend) AddComment(ctx context.Context, command workitems.AddCommentCommand) (*workitems.Comment, error) {
	// NB: per §6, comment bodies are PII-sensitive — only their length is
	// recorded, never the raw text.
	ctx, span := t.startServiceSpan(ctx, "WorkItems", "AddComment",
		attribute.String("loom.task_id", command.IssueID),
		attribute.Int("comment.bytes", len(command.Text)),
	)
	comments, ok := t.inner.(workitems.CommentCommands)
	if !ok {
		err := backend.ErrUnavailable("AddComment", "work items comment commands unavailable", nil)
		endSpan(span, err)
		return nil, err
	}
	out, err := comments.AddComment(ctx, command)
	endSpan(span, err)
	return out, err
}

// --- Event operations ---

func (t *tracedIssueBackend) ListEvents(ctx context.Context, query workitems.ListEventsQuery) ([]*workitems.Event, error) {
	ctx, span := t.startServiceSpan(ctx, "WorkItems", "ListEvents",
		attribute.String("loom.task_id", query.IssueID),
		attribute.Int("limit", query.Limit),
	)
	events, ok := t.inner.(workitems.EventQueries)
	if !ok {
		err := backend.ErrUnavailable("ListEvents", "work items event queries unavailable", nil)
		endSpan(span, err)
		return nil, err
	}
	out, err := events.ListEvents(ctx, query)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	endSpan(span, err)
	return out, err
}

// --- Metadata ---

func (t *tracedIssueBackend) BackendName() string { return t.inner.BackendName() }
