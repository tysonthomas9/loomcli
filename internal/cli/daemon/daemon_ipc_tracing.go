package daemon

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// ipcTracerName is the instrumentation library name reported on every
// daemon-IPC span. Stable so dashboards filtering on it don't break.
const ipcTracerName = "github.com/tysonthomas9/loomcli/internal/cli/daemon"

// startIPCSpan starts a span at the daemon IPC dispatch point. Span name
// uses the IPC operation (low-cardinality enum: "claim", "update",
// "complete") so dashboards group cleanly by method. Caller is responsible
// for ending the span; descendants (backend calls, store ops) inherit the
// returned ctx as parent.
//
// workspace is read from the daemon supervisor (per-process scope). agent
// is the per-request AgentName from the IPC envelope. Both are
// allowlisted in the loom trace contract (§4.1, §4.2) — agent names and
// workspace keys are low-cardinality enums in this deployment.
//
// Trace-context propagation note: the AgentIPCRequest envelope used by
// loomcli (see daemon_ipc.go) has no traceparent field, and the
// Unix-socket transport carries no headers. As a result, spans started
// here are roots — IPC callers (agent subprocesses) appear as separate
// traces. Adding propagation would require adding a "traceparent" field
// to AgentIPCRequest and threading it through the IPC client; mirrored
// gap from fleet-db's RPC layer (see fleet-db/internal/rpc/tracing.go).
func (d *Daemon) startIPCSpan(ctx context.Context, method, agent string) (context.Context, trace.Span) {
	tracer := otel.GetTracerProvider().Tracer(ipcTracerName)
	attrs := make([]attribute.KeyValue, 0, 4)
	attrs = append(attrs,
		attribute.String("rpc.system", "loom.daemon.ipc"),
		attribute.String("rpc.method", method),
	)
	if agent != "" {
		attrs = append(attrs, attribute.String("loom.agent", agent))
	}
	if d.sup != nil && d.sup.WorkspaceID != "" {
		attrs = append(attrs, attribute.String("loom.workspace", d.sup.WorkspaceID))
	}
	return tracer.Start(ctx,
		"daemon.ipc."+method,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attrs...),
	)
}

// recordIPCErr maps a non-success AgentIPCResponse to the span and applies
// the low-cardinality status convention. No-op when resp.Success is true.
// Records a synthetic error built from resp.Error + resp.Kind so the
// span's exception event carries the same information the IPC client sees.
func recordIPCErr(span trace.Span, resp AgentIPCResponse) {
	if resp.Success {
		return
	}
	kind := resp.Kind
	if kind == "" {
		kind = string(backend.KindInternal)
	}
	span.SetAttributes(attribute.String("loom.error_kind", kind))
	span.RecordError(errors.New(resp.Error))
	span.SetStatus(codes.Error, "daemon.ipc.failed")
}
