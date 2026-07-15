//go:build testbackend

package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/tysonthomas9/loomcli/internal/cli/backendapi"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

type StreamingBackend = backendapi.StreamingBackend
type HealthCheckableBackend = backendapi.HealthCheckableBackend
type MetadataProvider = backendapi.MetadataProvider
type HealthStatus = backendapi.HealthStatus
type BackendMeta = backendapi.BackendMeta
type StreamEvent = backendapi.StreamEvent
type StreamUsage = backendapi.StreamUsage

// EchoInvocation records the parameters of a single test-backend call.
type EchoInvocation struct {
	WorkDir   string
	Prompt    string
	AgentName string
	Mode      string // "interactive", "non-interactive", or "streaming"
}

// EchoHandler is a function that produces output for an EchoBackend invocation.
// The handler writes its response to w and returns any error.
type EchoHandler func(inv EchoInvocation, w io.Writer) error

// EchoBackend is a test-only Backend that records invocations and delegates
// output generation to a configurable handler. It implements the core Backend
// interface plus the optional StreamingBackend, HealthCheckableBackend, and
// MetadataProvider interfaces.
type EchoBackend struct {
	mu          sync.Mutex
	handler     EchoHandler
	invocations []EchoInvocation
}

// Compile-time interface assertions.
var _ Backend = (*EchoBackend)(nil)
var _ StreamingBackend = (*EchoBackend)(nil)
var _ HealthCheckableBackend = (*EchoBackend)(nil)
var _ MetadataProvider = (*EchoBackend)(nil)

// ---------------------------------------------------------------------------
// Backend interface
// ---------------------------------------------------------------------------

// Name returns the backend identifier.
func (e *EchoBackend) Name() string { return "echo" }

// InvokeInteractive records the invocation and calls the handler with io.Discard.
func (e *EchoBackend) InvokeInteractive(workDir, prompt, agentName string) error {
	inv := EchoInvocation{
		WorkDir:   workDir,
		Prompt:    prompt,
		AgentName: agentName,
		Mode:      "interactive",
	}
	e.record(inv)

	e.mu.Lock()
	h := e.handler
	e.mu.Unlock()

	return h(inv, io.Discard)
}

// InvokeNonInteractive records the invocation, runs the handler through a pipe,
// and scans the output for display. The collector and shutdown channel are
// accepted for interface compliance but not actively used by the echo backend.
func (e *EchoBackend) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	inv := EchoInvocation{
		WorkDir:   workDir,
		Prompt:    prompt,
		AgentName: agentName,
		Mode:      "non-interactive",
	}
	e.record(inv)

	e.mu.Lock()
	h := e.handler
	e.mu.Unlock()

	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		err := h(inv, pw)
		pw.CloseWithError(err)
		errCh <- err
	}()

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // 10MB max like Claude backend
	for scanner.Scan() {
		line := scanner.Text()
		if collector != nil {
			collectEchoStreamUsage(line, collector)
		}
	}
	pr.Close()

	if err := scanner.Err(); err != nil {
		<-errCh // drain to unblock goroutine
		return fmt.Errorf("echo: scanner error: %w", err)
	}

	return <-errCh
}

// ---------------------------------------------------------------------------
// StreamingBackend interface
// ---------------------------------------------------------------------------

// InvokeStreaming records the invocation and returns a ReadCloser backed by an
// io.Pipe with the handler running in a goroutine.
func (e *EchoBackend) InvokeStreaming(ctx context.Context, workDir, prompt, agentName string) (io.ReadCloser, error) {
	inv := EchoInvocation{
		WorkDir:   workDir,
		Prompt:    prompt,
		AgentName: agentName,
		Mode:      "streaming",
	}
	e.record(inv)

	e.mu.Lock()
	h := e.handler
	e.mu.Unlock()

	pr, pw := io.Pipe()
	go func() {
		err := h(inv, pw)
		pw.CloseWithError(err)
	}()

	return pr, nil
}

// ---------------------------------------------------------------------------
// HealthCheckableBackend interface
// ---------------------------------------------------------------------------

// HealthCheck always reports a healthy status.
func (e *EchoBackend) HealthCheck() HealthStatus {
	return HealthStatus{
		Healthy:   true,
		Installed: true,
		Version:   "test",
		APIKeySet: true,
		Message:   "ready",
	}
}

// ---------------------------------------------------------------------------
// MetadataProvider interface
// ---------------------------------------------------------------------------

// Meta returns fixed metadata describing the echo backend.
func (e *EchoBackend) Meta() BackendMeta {
	return BackendMeta{
		DisplayName: "Echo",
		Version:     "test",
		Description: "Test-only echo backend",
		URL:         "",
		BinaryName:  "echo",
	}
}

// ---------------------------------------------------------------------------
// Test control methods
// ---------------------------------------------------------------------------

// SetHandler replaces the current handler. Safe for concurrent use.
func (e *EchoBackend) SetHandler(h EchoHandler) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handler = h
}

// Invocations returns a copy of all recorded invocations. Safe for concurrent use.
func (e *EchoBackend) Invocations() []EchoInvocation {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]EchoInvocation, len(e.invocations))
	copy(out, e.invocations)
	return out
}

// Reset clears all recorded invocations. Safe for concurrent use.
func (e *EchoBackend) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.invocations = nil
}

// record appends an invocation to the log under the mutex.
func (e *EchoBackend) record(inv EchoInvocation) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.invocations = append(e.invocations, inv)
}

// ---------------------------------------------------------------------------
// Pre-built handlers
// ---------------------------------------------------------------------------

// DefaultEchoHandler emits a single JSON result event indicating success.
var DefaultEchoHandler EchoHandler = func(_ EchoInvocation, w io.Writer) error {
	_, err := fmt.Fprintln(w, `{"type":"result","subtype":"success","result":"echo done"}`)
	return err
}

// ErrorHandler returns an EchoHandler that always returns the given error
// without writing any output.
func ErrorHandler(err error) EchoHandler {
	return func(_ EchoInvocation, _ io.Writer) error {
		return err
	}
}

// UsageHandler returns an EchoHandler that emits a stream-json event
// containing the specified token counts, matching the StreamEvent format
// used by the Claude backend.
func UsageHandler(inputTokens, outputTokens int64) EchoHandler {
	return func(_ EchoInvocation, w io.Writer) error {
		event := StreamEvent{
			Type: "message_delta",
			Usage: &StreamUsage{
				InputTokens:  inputTokens,
				OutputTokens: outputTokens,
			},
		}
		data, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("echo: marshal usage event: %w", err)
		}
		if _, err := fmt.Fprintln(w, string(data)); err != nil {
			return err
		}
		// Also emit the result line so the output is well-formed.
		_, err = fmt.Fprintln(w, `{"type":"result","subtype":"success","result":"echo done"}`)
		return err
	}
}

// SequenceHandler returns an EchoHandler that cycles through the provided
// handlers in order. Each call advances to the next handler; after the last
// handler is reached, subsequent calls reuse the last handler. Safe for
// concurrent use via atomic counter.
func SequenceHandler(handlers ...EchoHandler) EchoHandler {
	if len(handlers) == 0 {
		return DefaultEchoHandler
	}
	var idx atomic.Int32
	return func(inv EchoInvocation, w io.Writer) error {
		i := int(idx.Add(1) - 1)
		if i >= len(handlers) {
			i = len(handlers) - 1
		}
		return handlers[i](inv, w)
	}
}

// CountingHandler wraps an inner handler and increments the provided atomic
// counter each time the handler is invoked. Useful for verifying call counts
// in tests.
func CountingHandler(counter *atomic.Int32, inner EchoHandler) EchoHandler {
	return func(inv EchoInvocation, w io.Writer) error {
		counter.Add(1)
		return inner(inv, w)
	}
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// collectEchoStreamUsage mirrors collectClaudeStreamUsage for the echo backend.
func collectEchoStreamUsage(line string, collector *usage.Collector) {
	var event StreamEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return
	}
	if event.Usage != nil {
		var messageID string
		if event.Message != nil {
			messageID = event.Message.ID
		}
		collector.Accumulate(messageID,
			event.Usage.InputTokens,
			event.Usage.OutputTokens,
			event.Usage.CacheReadInputTokens,
			event.Usage.CacheCreationInputTokens,
		)
	}
}

// ---------------------------------------------------------------------------
// Self-registration
// ---------------------------------------------------------------------------

var defaultEchoBackend = &EchoBackend{handler: DefaultEchoHandler}

func init() {
	RegisterBackend(defaultEchoBackend)
}
