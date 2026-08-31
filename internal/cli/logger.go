package cli

import (
	"context"
	"fmt"
	"io"
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

	slog.SetDefault(slog.New(NewSlogHandler(w, format)))

	// Bridge existing log.Printf calls through slog
	slog.SetLogLoggerLevel(slog.LevelInfo)

	return nil
}

// LogFormat returns the log format the process was started with, so a caller
// that re-installs the default handler over a different writer keeps the same
// line shape.
func LogFormat() string { return logFormat }

// NewSlogHandler builds the handler shape InitLogger installs: JSON or text
// (text for any unrecognized format), wrapped so trace_id / span_id from the
// context are injected when an active span is present.
func NewSlogHandler(w io.Writer, format string) slog.Handler {
	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}

	var handler slog.Handler
	switch format {
	case "json":
		handler = slog.NewJSONHandler(w, opts)
	default:
		handler = slog.NewTextHandler(w, opts)
	}

	// Pass-through when no active span is present in context, so non-traced
	// runs are unchanged.
	return &traceContextHandler{inner: handler}
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
