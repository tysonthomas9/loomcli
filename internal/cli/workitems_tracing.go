package cli

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/observability/tracing"
)

const workItemsTracerName = "github.com/tysonthomas9/loomcli/internal/cli/workitems"

type tracedWorkItems struct {
	inner       workitems.API
	backendAttr attribute.KeyValue
}

var _ workitems.API = (*tracedWorkItems)(nil)
var _ workitems.StatsQueries = (*tracedWorkItems)(nil)

type workItemBackendNamer interface {
	BackendName() string
}

func wrapWorkItemsWithTracing(inner workitems.API) workitems.API {
	if inner == nil {
		return nil
	}
	name := "unknown"
	if named, ok := inner.(workItemBackendNamer); ok {
		name = named.BackendName()
	}
	return &tracedWorkItems{inner: inner, backendAttr: attribute.String("loom.backend", name)}
}

func (t *tracedWorkItems) BackendName() string {
	if named, ok := t.inner.(workItemBackendNamer); ok {
		return named.BackendName()
	}
	return "unknown"
}

func (t *tracedWorkItems) start(ctx context.Context, method string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	ctx, span := tracing.Tracer(workItemsTracerName).Start(ctx, "service.WorkItems."+method)
	if span.IsRecording() {
		span.SetAttributes(t.backendAttr)
		span.SetAttributes(attrs...)
	}
	return ctx, span
}

func endWorkItemsSpan(span trace.Span, err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		span.RecordError(err)
		span.SetStatus(codes.Error, workItemsErrorReason(err))
	}
	span.End()
}

func workItemsErrorReason(err error) string {
	switch {
	case errors.Is(err, workitems.ErrNotFound):
		return "not_found"
	case errors.Is(err, workitems.ErrConflict):
		return "conflict"
	case errors.Is(err, workitems.ErrInvalid):
		return "validation"
	case errors.Is(err, workitems.ErrUnavailable):
		return "unavailable"
	case errors.Is(err, workitems.ErrNotImplemented):
		return "not_implemented"
	default:
		return "unknown"
	}
}

func (t *tracedWorkItems) Create(ctx context.Context, command workitems.CreateCommand) (*workitems.IssueSummary, error) {
	ctx, span := t.start(ctx, "Create")
	value, err := t.inner.Create(ctx, command)
	endWorkItemsSpan(span, err)
	return value, err
}

func (t *tracedWorkItems) List(ctx context.Context, query workitems.ListQuery) (*workitems.ListResult, error) {
	ctx, span := t.start(ctx, "List")
	value, err := t.inner.List(ctx, query)
	if err == nil && value != nil {
		span.SetAttributes(attribute.Int("result.count", len(value.Issues)+len(value.KanbanIssues)))
	}
	endWorkItemsSpan(span, err)
	return value, err
}

func (t *tracedWorkItems) Stats(ctx context.Context) (*workitems.Stats, error) {
	ctx, span := t.start(ctx, "Stats")
	queries, ok := t.inner.(workitems.StatsQueries)
	if !ok {
		err := workitems.ErrUnavailable
		endWorkItemsSpan(span, err)
		return nil, err
	}
	value, err := queries.Stats(ctx)
	endWorkItemsSpan(span, err)
	return value, err
}

func (t *tracedWorkItems) Ready(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	ctx, span := t.start(ctx, "Ready")
	value, err := t.inner.Ready(ctx, query)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(value)))
	}
	endWorkItemsSpan(span, err)
	return value, err
}

func (t *tracedWorkItems) Blocked(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	ctx, span := t.start(ctx, "Blocked")
	value, err := t.inner.Blocked(ctx, query)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(value)))
	}
	endWorkItemsSpan(span, err)
	return value, err
}

func (t *tracedWorkItems) Search(ctx context.Context, query workitems.SearchQuery) ([]workitems.IssueSummary, error) {
	ctx, span := t.start(ctx, "Search", attribute.Int("query.bytes", len(query.Query)), attribute.Int("limit", query.Limit))
	value, err := t.inner.Search(ctx, query)
	if err == nil {
		span.SetAttributes(attribute.Int("result.count", len(value)))
	}
	endWorkItemsSpan(span, err)
	return value, err
}

func (t *tracedWorkItems) Get(ctx context.Context, query workitems.GetQuery) (*workitems.IssueDetail, error) {
	ctx, span := t.start(ctx, "Get", attribute.String("loom.task_id", query.IssueID))
	value, err := t.inner.Get(ctx, query)
	endWorkItemsSpan(span, err)
	return value, err
}

func (t *tracedWorkItems) Patch(ctx context.Context, command workitems.PatchCommand) (*workitems.IssueDetail, error) {
	ctx, span := t.start(ctx, "Patch", attribute.String("loom.task_id", command.IssueID))
	value, err := t.inner.Patch(ctx, command)
	endWorkItemsSpan(span, err)
	return value, err
}

func (t *tracedWorkItems) Close(ctx context.Context, command workitems.CloseCommand) (*workitems.CloseResult, error) {
	ctx, span := t.start(ctx, "Close", attribute.String("loom.task_id", command.IssueID))
	value, err := t.inner.Close(ctx, command)
	if err == nil && value != nil {
		span.SetAttributes(attribute.Int("unblocked.count", len(value.Unblocked)))
	}
	endWorkItemsSpan(span, err)
	return value, err
}

func (t *tracedWorkItems) Claim(ctx context.Context, command workitems.ClaimCommand) (*workitems.IssueDetail, error) {
	ctx, span := t.start(ctx, "Claim", attribute.String("loom.task_id", command.IssueID))
	value, err := t.inner.Claim(ctx, command)
	endWorkItemsSpan(span, err)
	return value, err
}

func (t *tracedWorkItems) Reopen(ctx context.Context, command workitems.ReopenCommand) error {
	ctx, span := t.start(ctx, "Reopen", attribute.String("loom.task_id", command.IssueID))
	err := t.inner.Reopen(ctx, command)
	endWorkItemsSpan(span, err)
	return err
}

func (t *tracedWorkItems) BlockRepositoryRequired(ctx context.Context, command workitems.BlockRepositoryRequiredCommand) (*workitems.RepositoryAdmissionResult, error) {
	ctx, span := t.start(ctx, "BlockRepositoryRequired", attribute.String("loom.task_id", command.IssueID))
	value, err := t.inner.BlockRepositoryRequired(ctx, command)
	endWorkItemsSpan(span, err)
	return value, err
}

func (t *tracedWorkItems) AssignRepository(ctx context.Context, command workitems.AssignRepositoryCommand) (*workitems.IssueSummary, error) {
	ctx, span := t.start(ctx, "AssignRepository", attribute.String("loom.task_id", command.IssueID))
	value, err := t.inner.AssignRepository(ctx, command)
	endWorkItemsSpan(span, err)
	return value, err
}

func (t *tracedWorkItems) Delete(ctx context.Context, command workitems.DeleteCommand) (workitems.DeleteResult, error) {
	ctx, span := t.start(ctx, "Delete", attribute.String("loom.task_id", command.IssueID))
	value, err := t.inner.Delete(ctx, command)
	endWorkItemsSpan(span, err)
	return value, err
}

func (t *tracedWorkItems) ListEvents(ctx context.Context, query workitems.ListEventsQuery) ([]*workitems.Event, error) {
	ctx, span := t.start(ctx, "ListEvents", attribute.String("loom.task_id", query.IssueID), attribute.Int("limit", query.Limit))
	value, err := t.inner.ListEvents(ctx, query)
	endWorkItemsSpan(span, err)
	return value, err
}

func (t *tracedWorkItems) AddComment(ctx context.Context, command workitems.AddCommentCommand) (*workitems.Comment, error) {
	ctx, span := t.start(ctx, "AddComment", attribute.String("loom.task_id", command.IssueID), attribute.Int("comment.bytes", len(command.Text)))
	value, err := t.inner.AddComment(ctx, command)
	endWorkItemsSpan(span, err)
	return value, err
}

func (t *tracedWorkItems) ListComments(ctx context.Context, query workitems.ListCommentsQuery) ([]*workitems.Comment, error) {
	ctx, span := t.start(ctx, "ListComments", attribute.String("loom.task_id", query.IssueID))
	value, err := t.inner.ListComments(ctx, query)
	endWorkItemsSpan(span, err)
	return value, err
}

func (t *tracedWorkItems) AddDependency(ctx context.Context, command workitems.AddDependencyCommand) error {
	ctx, span := t.start(ctx, "AddDependency", attribute.String("loom.task_id", command.IssueID))
	err := t.inner.AddDependency(ctx, command)
	endWorkItemsSpan(span, err)
	return err
}

func (t *tracedWorkItems) RemoveDependency(ctx context.Context, command workitems.RemoveDependencyCommand) error {
	ctx, span := t.start(ctx, "RemoveDependency", attribute.String("loom.task_id", command.IssueID))
	err := t.inner.RemoveDependency(ctx, command)
	endWorkItemsSpan(span, err)
	return err
}

func (t *tracedWorkItems) ListDependencies(ctx context.Context, query workitems.ListDependenciesQuery) ([]workitems.Dependency, error) {
	ctx, span := t.start(ctx, "ListDependencies", attribute.String("loom.task_id", query.IssueID))
	value, err := t.inner.ListDependencies(ctx, query)
	endWorkItemsSpan(span, err)
	return value, err
}
