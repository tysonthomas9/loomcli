package otelexport

import (
	"context"
	"log"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/events"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Attribute keys for OTel spans and metrics.
const (
	AttrAgent     = attribute.Key("loom.agent")
	AttrRole      = attribute.Key("loom.role")
	AttrEpicID    = attribute.Key("loom.epic_id")
	AttrTaskID    = attribute.Key("loom.task_id")
	AttrErrorType = attribute.Key("loom.error_type")
	AttrPID       = attribute.Key("loom.pid")
	AttrExitCode  = attribute.Key("loom.exit_code")
)

// Exporter subscribes to the event Bus and pushes metrics/traces to an OTLP collector.
type Exporter struct {
	config Config

	tracerProvider *sdktrace.TracerProvider
	meterProvider  *sdkmetric.MeterProvider

	tracer           trace.Tracer
	taskCompleted    metric.Int64Counter
	taskFailed       metric.Int64Counter
	agentRestart     metric.Int64Counter
	taskDuration     metric.Float64Histogram
	taskLinesChanged metric.Int64Histogram

	mu               sync.Mutex
	activeAgentSpans map[string]trace.Span
	activeTaskSpans  map[string]trace.Span

	stopOnce sync.Once
}

// ProviderOption allows injecting custom providers for testing.
type ProviderOption func(*Exporter)

// WithTracerProvider overrides the default TracerProvider.
func WithTracerProvider(tp *sdktrace.TracerProvider) ProviderOption {
	return func(e *Exporter) {
		e.tracerProvider = tp
	}
}

// WithMeterProvider overrides the default MeterProvider.
func WithMeterProvider(mp *sdkmetric.MeterProvider) ProviderOption {
	return func(e *Exporter) {
		e.meterProvider = mp
	}
}

// New creates an Exporter with OTLP HTTP exporters for traces and metrics.
func New(cfg Config, opts ...ProviderOption) (*Exporter, error) {
	cfg = cfg.Resolved()
	ctx := context.Background()

	res, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
		),
	)
	if err != nil {
		return nil, err
	}

	e := &Exporter{
		config:           cfg,
		activeAgentSpans: make(map[string]trace.Span),
		activeTaskSpans:  make(map[string]trace.Span),
	}

	for _, opt := range opts {
		opt(e)
	}

	// Initialize TracerProvider
	if cfg.TracesEnabled() && e.tracerProvider == nil {
		traceExp, err := otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(stripScheme(cfg.Endpoint)),
			otlptracehttp.WithInsecure(),
		)
		if err != nil {
			return nil, err
		}
		e.tracerProvider = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExp),
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRate))),
		)
	}

	// Initialize MeterProvider
	if cfg.MetricsEnabled() && e.meterProvider == nil {
		metricExp, err := otlpmetrichttp.New(ctx,
			otlpmetrichttp.WithEndpoint(stripScheme(cfg.Endpoint)),
			otlpmetrichttp.WithInsecure(),
		)
		if err != nil {
			if e.tracerProvider != nil {
				_ = e.tracerProvider.Shutdown(ctx)
			}
			return nil, err
		}
		e.meterProvider = sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp,
				sdkmetric.WithInterval(cfg.FlushInterval),
			)),
			sdkmetric.WithResource(res),
		)
	}

	// Get tracer and meter from providers (or noop)
	var tracer trace.Tracer
	if e.tracerProvider != nil {
		tracer = e.tracerProvider.Tracer("loomcli")
	} else {
		tracer = tracenoop.NewTracerProvider().Tracer("loomcli")
	}
	e.tracer = tracer

	var meter metric.Meter
	if e.meterProvider != nil {
		meter = e.meterProvider.Meter("loomcli")
	} else {
		meter = metricnoop.NewMeterProvider().Meter("loomcli")
	}

	e.taskCompleted, _ = meter.Int64Counter("loom.task.completed")
	e.taskFailed, _ = meter.Int64Counter("loom.task.failed")
	e.agentRestart, _ = meter.Int64Counter("loom.agent.restart")
	e.taskDuration, _ = meter.Float64Histogram("loom.task.duration_ms",
		metric.WithUnit("ms"),
	)
	e.taskLinesChanged, _ = meter.Int64Histogram("loom.task.lines_changed")

	return e, nil
}

// HandleEvent is the Listener callback for the event Bus.
func (e *Exporter) HandleEvent(ev events.Event) {
	switch ev.Type {
	case events.TaskClaimed:
		e.handleTaskClaimed(ev)
	case events.TaskCompleted:
		e.handleTaskCompleted(ev)
	case events.TaskFailed:
		e.handleTaskFailed(ev)
	case events.AgentStarted:
		e.handleAgentStarted(ev)
	case events.AgentStopped:
		e.handleAgentStopped(ev)
	case events.AgentRestarted:
		e.handleAgentRestarted(ev)
	}
}

func (e *Exporter) handleTaskClaimed(ev events.Event) {
	data, ok := decodeData[events.TaskClaimedData](ev)
	if !ok {
		return
	}

	_, span := e.tracer.Start(context.Background(), "loom.task",
		trace.WithAttributes(
			AttrAgent.String(ev.Agent),
			AttrRole.String(ev.Role),
			AttrEpicID.String(ev.EpicID),
			AttrTaskID.String(data.TaskID),
		),
	)

	e.mu.Lock()
	prev := e.activeTaskSpans[ev.Agent]
	e.activeTaskSpans[ev.Agent] = span
	e.mu.Unlock()

	if prev != nil {
		prev.SetStatus(codes.Error, "superseded")
		prev.End()
	}
}

func (e *Exporter) handleTaskCompleted(ev events.Event) {
	data, ok := decodeData[events.TaskCompletedData](ev)
	if !ok {
		return
	}

	attrs := []attribute.KeyValue{
		AttrAgent.String(ev.Agent),
		AttrRole.String(ev.Role),
		AttrEpicID.String(ev.EpicID),
	}

	ctx := context.Background()
	e.taskCompleted.Add(ctx, 1, metric.WithAttributes(attrs...))
	e.taskDuration.Record(ctx, float64(data.Duration.Duration.Milliseconds()), metric.WithAttributes(attrs...))
	e.taskLinesChanged.Record(ctx, int64(data.LinesAdded+data.LinesRemoved), metric.WithAttributes(attrs...))

	e.mu.Lock()
	if span, ok := e.activeTaskSpans[ev.Agent]; ok {
		span.End()
		delete(e.activeTaskSpans, ev.Agent)
	}
	e.mu.Unlock()
}

func (e *Exporter) handleTaskFailed(ev events.Event) {
	data, ok := decodeData[events.TaskFailedData](ev)
	if !ok {
		return
	}

	attrs := []attribute.KeyValue{
		AttrAgent.String(ev.Agent),
		AttrRole.String(ev.Role),
		AttrErrorType.String(categorizeError(data.Error)),
	}

	e.taskFailed.Add(context.Background(), 1, metric.WithAttributes(attrs...))

	e.mu.Lock()
	if span, ok := e.activeTaskSpans[ev.Agent]; ok {
		span.SetStatus(codes.Error, categorizeError(data.Error))
		span.End()
		delete(e.activeTaskSpans, ev.Agent)
	}
	e.mu.Unlock()
}

func (e *Exporter) handleAgentStarted(ev events.Event) {
	data, ok := decodeData[events.AgentStartedData](ev)
	if !ok {
		return
	}

	_, span := e.tracer.Start(context.Background(), "loom.agent.lifecycle",
		trace.WithAttributes(
			AttrAgent.String(ev.Agent),
			AttrRole.String(ev.Role),
			AttrPID.Int(data.PID),
		),
	)

	e.mu.Lock()
	prev := e.activeAgentSpans[ev.Agent]
	e.activeAgentSpans[ev.Agent] = span
	e.mu.Unlock()

	if prev != nil {
		prev.SetStatus(codes.Error, "superseded")
		prev.End()
	}
}

func (e *Exporter) handleAgentStopped(ev events.Event) {
	data, ok := decodeData[events.AgentStoppedData](ev)
	if !ok {
		return
	}

	e.mu.Lock()
	if span, ok := e.activeAgentSpans[ev.Agent]; ok {
		span.SetAttributes(AttrExitCode.Int(data.ExitCode))
		span.End()
		delete(e.activeAgentSpans, ev.Agent)
	}
	e.mu.Unlock()
}

func (e *Exporter) handleAgentRestarted(ev events.Event) {
	attrs := []attribute.KeyValue{
		AttrAgent.String(ev.Agent),
		AttrRole.String(ev.Role),
	}
	e.agentRestart.Add(context.Background(), 1, metric.WithAttributes(attrs...))

	e.mu.Lock()
	taskSpan := e.activeTaskSpans[ev.Agent]
	delete(e.activeTaskSpans, ev.Agent)
	e.mu.Unlock()

	if taskSpan != nil {
		taskSpan.SetStatus(codes.Error, "agent_restarted")
		taskSpan.End()
	}
}

// Stop flushes all pending exports and ends any active spans.
func (e *Exporter) Stop(ctx context.Context) error {
	var stopErr error
	e.stopOnce.Do(func() {
		e.mu.Lock()
		for agent, span := range e.activeAgentSpans {
			span.SetStatus(codes.Ok, "shutdown")
			span.End()
			delete(e.activeAgentSpans, agent)
		}
		for agent, span := range e.activeTaskSpans {
			span.SetStatus(codes.Ok, "shutdown")
			span.End()
			delete(e.activeTaskSpans, agent)
		}
		e.mu.Unlock()

		if e.tracerProvider != nil {
			if err := e.tracerProvider.Shutdown(ctx); err != nil {
				stopErr = err
			}
		}
		if e.meterProvider != nil {
			if err := e.meterProvider.Shutdown(ctx); err != nil && stopErr == nil {
				stopErr = err
			}
		}
	})
	return stopErr
}

// decodeData decodes the event Data field into the specified type.
func decodeData[T any](ev events.Event) (*T, bool) {
	raw, err := ev.DecodeData()
	if err != nil {
		log.Printf("[otel] error decoding %s event: %v", ev.Type, err)
		return nil, false
	}
	data, ok := raw.(*T)
	if !ok {
		return nil, false
	}
	return data, true
}

// categorizeError maps raw error messages to safe categories.
func categorizeError(errMsg string) string {
	lower := strings.ToLower(errMsg)
	switch {
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline"):
		return "timeout"
	case strings.Contains(lower, "oom") || strings.Contains(lower, "out of memory"):
		return "oom"
	case strings.Contains(lower, "permission") || strings.Contains(lower, "access denied"):
		return "permission"
	case strings.Contains(lower, "network") || strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "dns") || strings.Contains(lower, "unreachable"):
		return "network"
	case strings.Contains(lower, "crash") || strings.Contains(lower, "segfault") ||
		strings.Contains(lower, "panic"):
		return "crash"
	default:
		return "unknown"
	}
}

// stripScheme removes http:// or https:// from an endpoint URL for the OTel SDK.
func stripScheme(endpoint string) string {
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")
	return endpoint
}
