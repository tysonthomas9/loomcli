package cli

import (
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/observability/tracing"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// agentInvokerTracerName is the instrumentation library name reported on
// every backend-invoke span. Stable so dashboards filtering on it don't
// break.
const agentInvokerTracerName = "github.com/tysonthomas9/loomcli/internal/cli/backend"

// tracedAgentInvoker wraps an AgentInvoker and emits one span per Invoke*
// call. Spans nest under the active root span (typically loom.cli.plan or
// loom.cli.task) via cmdstore.RootContext, giving sub-task granularity for
// "how long was spent in the LLM" vs other work the agent does.
//
// Per the trace contract §6, prompt content is NEVER recorded as a span
// attribute. Only prompt.bytes is captured. Same for usage tokens (numeric
// only, no text).
type tracedAgentInvoker struct {
	inner AgentInvoker
}

// wrapAgentInvokerWithTracing returns a tracing-decorated AgentInvoker.
// When the global TracerProvider is no-op (tracing disabled), the overhead
// is one span construction + immediate end per call. Cheap.
func wrapAgentInvokerWithTracing(inner AgentInvoker) AgentInvoker {
	if inner == nil {
		return nil
	}
	return &tracedAgentInvoker{inner: inner}
}

func (t *tracedAgentInvoker) InvokeInteractive(workDir, prompt, agentName string) error {
	tracer := tracing.Tracer(agentInvokerTracerName)
	ctx, span := tracer.Start(cmdstore.RootContext(),
		"loom.backend."+GetBackendName()+".invoke_interactive",
		trace.WithAttributes(
			attribute.String("loom.agent", agentName),
			attribute.String("loom.task_id", os.Getenv("LOOM_ASSIGNED_TASK_ID")),
			attribute.String("loom.backend", GetBackendName()),
			attribute.String("backend.mode", "interactive"),
			attribute.Int("prompt.bytes", len(prompt)),
		),
	)
	defer span.End()
	_ = ctx
	err := t.inner.InvokeInteractive(workDir, prompt, agentName)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "backend.invoke_failed")
	}
	return err
}

func (t *tracedAgentInvoker) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	tracer := tracing.Tracer(agentInvokerTracerName)
	_, span := tracer.Start(cmdstore.RootContext(),
		"loom.backend."+GetBackendName()+".invoke_non_interactive",
		trace.WithAttributes(
			attribute.String("loom.agent", agentName),
			attribute.String("loom.task_id", os.Getenv("LOOM_ASSIGNED_TASK_ID")),
			attribute.String("loom.backend", GetBackendName()),
			attribute.String("backend.mode", "non_interactive"),
			attribute.Int("prompt.bytes", len(prompt)),
		),
	)
	defer func() {
		// Capture usage on completion. Numeric-only (no PII).
		if collector != nil {
			in, out, cacheR, cacheW := collector.Totals()
			span.SetAttributes(
				attribute.Int64("loom.usage.input_tokens", in),
				attribute.Int64("loom.usage.output_tokens", out),
				attribute.Int64("loom.usage.cache_read_tokens", cacheR),
				attribute.Int64("loom.usage.cache_write_tokens", cacheW),
			)
		}
		span.End()
	}()
	err := t.inner.InvokeNonInteractive(workDir, prompt, agentName, shutdown, collector)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "backend.invoke_failed")
	}
	return err
}
