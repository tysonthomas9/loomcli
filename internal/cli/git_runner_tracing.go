package cli

import (
	"context"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/observability/tracing"
)

// gitRunnerTracerName is the instrumentation library name reported on every
// git-subprocess span. Stable so dashboards filtering on it don't break.
const gitRunnerTracerName = "github.com/tysonthomas9/loomcli/internal/cli/git"

// tracedGitRunner wraps a GitRunner and emits one span per git subprocess
// invocation. Spans nest under the active root span (typically loom.cli.<verb>)
// via cmdstore.RootContext, giving sub-task granularity for "how long was
// spent in git" vs other work.
//
// Per the trace contract §6, raw arg lists are NEVER recorded as a span
// attribute (they may contain user-controlled refs, URLs, or refspecs).
// Only the bounded git subcommand verb (low cardinality) and a numeric
// arg count are captured.
type tracedGitRunner struct {
	inner GitRunner
}

// wrapGitRunnerWithTracing returns a tracing-decorated GitRunner. When the
// global TracerProvider is no-op (tracing disabled), the overhead is one
// span construction + immediate end per call.
func wrapGitRunnerWithTracing(inner GitRunner) GitRunner {
	if inner == nil {
		return nil
	}
	return &tracedGitRunner{inner: inner}
}

// gitSpanName returns the span name "git.<subcommand>" for the leading
// argument. If the first arg is empty, missing, or contains characters
// outside the bounded verb set, returns "git.unknown" so the cardinality
// allowlist is preserved (see internal/observability/tracing/cardinality_test.go).
func gitSpanName(args []string) string {
	if len(args) == 0 {
		return "git.unknown"
	}
	verb := strings.ToLower(strings.TrimSpace(args[0]))
	if verb == "" {
		return "git.unknown"
	}
	for _, r := range verb {
		// Bounded set: lowercase letters, dash, underscore. Matches the
		// allowlist regex `^git\.[a-z][a-z_-]*$`. Anything else falls back
		// to "unknown" (e.g. flags like `-c`, refs glued in by mistake).
		if (r >= 'a' && r <= 'z') || r == '-' || r == '_' {
			continue
		}
		return "git.unknown"
	}
	if verb[0] < 'a' || verb[0] > 'z' {
		return "git.unknown"
	}
	return "git." + verb
}

// gitSpanAttrs returns the safe (non-PII) attributes for a git subprocess
// span. Intentionally excludes the raw arg list — only the verb and a
// numeric arg count are recorded.
func gitSpanAttrs(args []string) []attribute.KeyValue {
	verb := ""
	if len(args) > 0 {
		verb = strings.ToLower(strings.TrimSpace(args[0]))
	}
	return []attribute.KeyValue{
		attribute.String("git.command", verb),
		attribute.Int("git.arg_count", len(args)),
	}
}

// recordGitSpanResult attaches the bounded result attributes (exit_code,
// duration_ms) and, on error, records the error and sets the categorized
// span status. Output bytes are deliberately NOT recorded (commit messages
// and diffs are PII per the trace contract).
func recordGitSpanResult(span trace.Span, start time.Time, exitCode int, err error) {
	span.SetAttributes(
		attribute.Int("git.exit_code", exitCode),
		attribute.Int64("git.duration_ms", time.Since(start).Milliseconds()),
	)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "git.failed")
	}
}

func (t *tracedGitRunner) Run(dir string, args ...string) CommandResult {
	tracer := tracing.Tracer(gitRunnerTracerName)
	ctx, span := tracer.Start(cmdstore.RootContext(),
		gitSpanName(args),
		trace.WithAttributes(gitSpanAttrs(args)...),
	)
	defer span.End()
	_ = ctx
	start := time.Now()
	res := t.inner.Run(dir, args...)
	recordGitSpanResult(span, start, exitCodeFor(res.Err), res.Err)
	return res
}

func (t *tracedGitRunner) RunContext(ctx context.Context, dir string, args ...string) CommandResult {
	tracer := tracing.Tracer(gitRunnerTracerName)
	ctx, span := tracer.Start(ctx,
		gitSpanName(args),
		trace.WithAttributes(gitSpanAttrs(args)...),
	)
	defer span.End()
	start := time.Now()
	inner, ok := t.inner.(ContextGitRunner)
	if !ok {
		err := context.Canceled
		recordGitSpanResult(span, start, exitCodeFor(err), err)
		return CommandResult{Err: err}
	}
	res := inner.RunContext(ctx, dir, args...)
	recordGitSpanResult(span, start, exitCodeFor(res.Err), res.Err)
	return res
}

func (t *tracedGitRunner) RunWithOutput(dir string, args ...string) error {
	tracer := tracing.Tracer(gitRunnerTracerName)
	ctx, span := tracer.Start(cmdstore.RootContext(),
		gitSpanName(args),
		trace.WithAttributes(gitSpanAttrs(args)...),
	)
	defer span.End()
	_ = ctx
	start := time.Now()
	err := t.inner.RunWithOutput(dir, args...)
	recordGitSpanResult(span, start, exitCodeFor(err), err)
	return err
}

// exitCodeFor extracts a process exit code from an error returned by
// os/exec. Returns 0 on success, the OS-reported exit code on a clean
// non-zero exit, and -1 when no exit code is available (signal, spawn
// failure, etc.). Bounded numeric — safe as a span attribute.
func exitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	type exitCoder interface {
		ExitCode() int
	}
	if ec, ok := err.(exitCoder); ok {
		return ec.ExitCode()
	}
	return -1
}
