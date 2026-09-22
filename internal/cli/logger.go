package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/trace"
)

// logFormat and logOutput are bound to persistent CLI flags in root.go.
var (
	logFormat string
	logOutput string
)

// InitLogger configures the process-wide default slog logger.
// format: "text" (default) or "json"
// output: "stderr" (default) or a file path
func InitLogger(format, output string) error {
	return initLogger(format, output, slog.LevelInfo)
}

func initLogger(format, output string, level slog.Level) error {
	// Determine writer
	var w *os.File
	switch output {
	case "", "stderr":
		w = os.Stderr
	default:
		f, err := os.OpenFile(output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600) // #nosec G304 - path from --log-output CLI flag
		if err != nil {
			return fmt.Errorf("failed to open log file %s: %w", output, err)
		}
		w = f
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	var handler slog.Handler
	switch format {
	case "json":
		handler = slog.NewJSONHandler(w, opts)
	default:
		// "text" or any unrecognized value defaults to text
		handler = slog.NewTextHandler(w, opts)
	}

	// Inject trace_id / span_id from context. Pass-through when no active
	// span is present in context, so non-traced runs are unchanged.
	handler = &traceContextHandler{inner: handler}

	slog.SetDefault(slog.New(handler))

	// Bridge existing log.Printf calls through slog
	// Legacy log.Printf messages are informational. Keep that bridge level
	// stable so a warn-level handler suppresses them with other INFO output.
	slog.SetLogLoggerLevel(slog.LevelInfo)

	return nil
}

type traceContextHandler struct {
	inner slog.Handler
}

func (h *traceContextHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

func (h *traceContextHandler) Handle(ctx context.Context, r slog.Record) error {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return h.inner.Handle(ctx, r)
	}
	r.AddAttrs(
		slog.String("trace_id", sc.TraceID().String()),
		slog.String("span_id", sc.SpanID().String()),
	)
	return h.inner.Handle(ctx, r)
}

func (h *traceContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceContextHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *traceContextHandler) WithGroup(name string) slog.Handler {
	return &traceContextHandler{inner: h.inner.WithGroup(name)}
}
