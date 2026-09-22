package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/trace"
)

type commandExitError struct {
	code int
	err  error
}

func (e *commandExitError) Error() string    { return e.err.Error() }
func (e *commandExitError) Unwrap() error    { return e.err }
func (e *commandExitError) CLIExitCode() int { return e.code }

// NewCommandExitError returns an error that asks the loom executable to use
// code while preserving err for display and errors.Is/errors.As.
func NewCommandExitError(code int, err error) error {
	if err == nil {
		err = fmt.Errorf("command exited with status %d", code)
	}
	return &commandExitError{code: code, err: err}
}

// CommandExitCode returns the requested process exit code, or 1 for ordinary
// command errors. A non-positive requested code is treated as an ordinary
// failure: a command asking to exit 0 through the error path is asking for a
// success the caller does not have.
func CommandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var coded interface{ CLIExitCode() int }
	if errors.As(err, &coded) && coded.CLIExitCode() > 0 {
		return coded.CLIExitCode()
	}
	return 1
}

// activeRootSpan and activeTraceShutdown are populated by Execute() with the
// per-invocation root span and trace-provider shutdown function. ExitWithFlush
// reads them to end the root span and flush exporters before the process
// terminates via os.Exit.
//
// Stored as atomic values so concurrent goroutines (e.g., the signal handler
// in Execute and a Cobra Run handler racing to exit) can read them without a
// lock. Both are nil before tracing init and a no-op after shutdown.
var (
	activeRootSpan      atomic.Value // trace.Span
	activeTraceShutdown atomic.Value // func(context.Context) error
)

// RegisterActiveTraceState publishes the per-invocation tracing state so
// ExitWithFlush can find it from any goroutine. Called from cli.Execute
// after tracing.Init.
func RegisterActiveTraceState(span trace.Span, shutdown func(context.Context) error) {
	activeRootSpan.Store(span)
	activeTraceShutdown.Store(shutdown)
}

// ExitWithFlush ends the active root span, flushes the trace provider with a
// short timeout, flushes the agent event bus, and then calls os.Exit(code). Use
// this from CLI handlers instead of os.Exit when traces and host-local event
// JSONL should be exported on the failure path. Safe to call from any
// goroutine; multiple concurrent callers are idempotent (the underlying
// provider's and event writer's Shutdown/Close operations are idempotent).
func ExitWithFlush(code int) {
	if v := activeRootSpan.Load(); v != nil {
		if span, ok := v.(trace.Span); ok && span != nil {
			span.End()
		}
	}
	// JSONLWriter is buffered, so it must be closed before os.Exit bypasses
	// Execute's normal-return defers. CloseAgentEventBus is idempotent.
	CloseAgentEventBus(context.Background())
	if v := activeTraceShutdown.Load(); v != nil {
		if fn, ok := v.(func(context.Context) error); ok && fn != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = fn(ctx)
			cancel()
		}
	}
	os.Exit(code)
}
