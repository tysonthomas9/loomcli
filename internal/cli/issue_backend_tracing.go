package cli

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/tysonthomas9/loomcli/internal/backend"
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
	tracer := tracing.Tracer(issueBackendTracerName)
	ctx, span := tracer.Start(ctx, "service.IssueBackend."+method)
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

func (t *tracedIssueBackend) Ready(ctx context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	ctx, span := t.startSpan(ctx, "Ready")
	out, err := t.inner.Ready(ctx, opts)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	endSpan(span, err)
	return out, err
}

func (t *tracedIssueBackend) Blocked(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	ctx, span := t.startSpan(ctx, "Blocked")
	out, err := t.inner.Blocked(ctx, opts)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	endSpan(span, err)
	return out, err
}

func (t *tracedIssueBackend) Stats(ctx context.Context) (*backend.StatsData, error) {
	ctx, span := t.startSpan(ctx, "Stats")
	out, err := t.inner.Stats(ctx)
	endSpan(span, err)
	return out, err
}

func (t *tracedIssueBackend) Count(ctx context.Context, opts backend.CountOpts) (int, error) {
	ctx, span := t.startSpan(ctx, "Count")
	out, err := t.inner.Count(ctx, opts)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", out))
	}
	endSpan(span, err)
	return out, err
}

func (t *tracedIssueBackend) GetChildren(ctx context.Context, id string) ([]backend.IssueData, error) {
	ctx, span := t.startSpan(ctx, "GetChildren",
		attribute.String("loom.task_id", id),
	)
	out, err := t.inner.GetChildren(ctx, id)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	endSpan(span, err)
	return out, err
}

func (t *tracedIssueBackend) SearchIssues(ctx context.Context, query string, limit int) ([]backend.IssueData, error) {
	// NB: per §6, query content is PII-sensitive — only its length is
	// recorded as an attribute, never the raw string.
	ctx, span := t.startSpan(ctx, "SearchIssues",
		attribute.Int("query.bytes", len(query)),
		attribute.Int("limit", limit),
	)
	out, err := t.inner.SearchIssues(ctx, query, limit)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	endSpan(span, err)
	return out, err
}

// --- Mutation operations ---

func (t *tracedIssueBackend) Create(ctx context.Context, params backend.CreateParams) (*backend.CreateResult, error) {
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

func (t *tracedIssueBackend) DeferIssue(ctx context.Context, id string, until time.Time) error {
	ctx, span := t.startSpan(ctx, "DeferIssue",
		attribute.String("loom.task_id", id),
	)
	err := t.inner.DeferIssue(ctx, id, until)
	endSpan(span, err)
	return err
}

func (t *tracedIssueBackend) UndeferIssue(ctx context.Context, id string) error {
	ctx, span := t.startSpan(ctx, "UndeferIssue",
		attribute.String("loom.task_id", id),
	)
	err := t.inner.UndeferIssue(ctx, id)
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

func (t *tracedIssueBackend) AddDependency(ctx context.Context, params backend.DepAddParams) error {
	ctx, span := t.startSpan(ctx, "AddDependency")
	err := t.inner.AddDependency(ctx, params)
	endSpan(span, err)
	return err
}

func (t *tracedIssueBackend) RemoveDependency(ctx context.Context, params backend.DepRemoveParams) error {
	ctx, span := t.startSpan(ctx, "RemoveDependency")
	err := t.inner.RemoveDependency(ctx, params)
	endSpan(span, err)
	return err
}

// --- Label operations ---

func (t *tracedIssueBackend) AddLabel(ctx context.Context, id string, label string) error {
	// NB: label names can be tag-like and PII-low risk, but the contract's
	// strict allowlist says low-cardinality only. Capture id (always
	// allowlisted) and label.bytes; skip the raw label value.
	ctx, span := t.startSpan(ctx, "AddLabel",
		attribute.String("loom.task_id", id),
		attribute.Int("label.bytes", len(label)),
	)
	err := t.inner.AddLabel(ctx, id, label)
	endSpan(span, err)
	return err
}

func (t *tracedIssueBackend) RemoveLabel(ctx context.Context, id string, label string) error {
	ctx, span := t.startSpan(ctx, "RemoveLabel",
		attribute.String("loom.task_id", id),
		attribute.Int("label.bytes", len(label)),
	)
	err := t.inner.RemoveLabel(ctx, id, label)
	endSpan(span, err)
	return err
}

// --- Comment operations ---

func (t *tracedIssueBackend) ListComments(ctx context.Context, id string) ([]backend.CommentData, error) {
	ctx, span := t.startSpan(ctx, "ListComments",
		attribute.String("loom.task_id", id),
	)
	out, err := t.inner.ListComments(ctx, id)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	endSpan(span, err)
	return out, err
}

func (t *tracedIssueBackend) AddComment(ctx context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
	// NB: per §6, comment bodies are PII-sensitive — only their length is
	// recorded, never the raw text.
	ctx, span := t.startSpan(ctx, "AddComment",
		attribute.String("loom.task_id", params.IssueID),
		attribute.Int("comment.bytes", len(params.Text)),
	)
	out, err := t.inner.AddComment(ctx, params)
	endSpan(span, err)
	return out, err
}

// --- Event operations ---

func (t *tracedIssueBackend) ListEvents(ctx context.Context, id string, limit int) ([]backend.EventData, error) {
	ctx, span := t.startSpan(ctx, "ListEvents",
		attribute.String("loom.task_id", id),
		attribute.Int("limit", limit),
	)
	out, err := t.inner.ListEvents(ctx, id, limit)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	endSpan(span, err)
	return out, err
}

// --- Batch operations ---

func (t *tracedIssueBackend) Batch(ctx context.Context, ops []backend.BatchOp) ([]backend.BatchResult, error) {
	ctx, span := t.startSpan(ctx, "Batch",
		attribute.Int("ops.count", len(ops)),
	)
	out, err := t.inner.Batch(ctx, ops)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	endSpan(span, err)
	return out, err
}

// --- Mutation polling ---

func (t *tracedIssueBackend) GetMutations(ctx context.Context, sinceMs int64) ([]backend.MutationData, error) {
	ctx, span := t.startSpan(ctx, "GetMutations")
	out, err := t.inner.GetMutations(ctx, sinceMs)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	endSpan(span, err)
	return out, err
}

func (t *tracedIssueBackend) WaitForMutations(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
	ctx, span := t.startSpan(ctx, "WaitForMutations",
		attribute.Int64("timeout_ms", timeoutMs),
	)
	out, err := t.inner.WaitForMutations(ctx, sinceMs, timeoutMs)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(out)))
	}
	endSpan(span, err)
	return out, err
}

// --- Metadata ---

func (t *tracedIssueBackend) BackendName() string { return t.inner.BackendName() }
