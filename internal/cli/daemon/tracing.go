package daemon

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// daemonTracerName is the instrumentation library name for spans emitted
// from this package. Stable so dashboards filtering on it don't break.
const daemonTracerName = "github.com/tysonthomas9/loomcli/internal/cli/daemon"

// startCommandPollSpan opens a span for one cycle of the agent command
// poller. Caller is responsible for span.End(); typically defer.
//
// Returns the new ctx so descendant operations (the AgentCommands().List
// call) inherit the span as parent.
func startCommandPollSpan(ctx context.Context) (context.Context, trace.Span) {
	tracer := otel.GetTracerProvider().Tracer(daemonTracerName)
	return tracer.Start(ctx, "daemon.command_poll.cycle",
		trace.WithSpanKind(trace.SpanKindInternal),
	)
}

// recordCommandPollErr records a poll-cycle error using the contract's
// low-cardinality status convention. No-op for nil.
func recordCommandPollErr(span trace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, "daemon.command_poll.failed")
}

// startMutationsCycleSpan opens a span for one drain cycle of the mutation
// buffer (one WaitSince invocation). Each cycle is bounded by the caller's
// timeout. Caller is responsible for span.End(); typically defer.
//
// Returns the new ctx so descendants (none today, but future inner ops)
// inherit the span as parent.
func startMutationsCycleSpan(ctx context.Context) (context.Context, trace.Span) {
	tracer := otel.GetTracerProvider().Tracer(daemonTracerName)
	return tracer.Start(ctx, "daemon.mutations.cycle",
		trace.WithSpanKind(trace.SpanKindInternal),
	)
}
