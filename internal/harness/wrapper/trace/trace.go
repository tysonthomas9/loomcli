// Package trace defines the diagnostic event vocabulary for the
// harness wrapper. Implementations of Emitter receive events
// describing the wrapper's internal lifecycle and can route them to
// stderr, log files, structured logging frameworks, or test recorders.
//
// Trace events are observability, not control flow. Callers should not
// make decisions based on event kinds, fields, or ordering — the trace
// vocabulary is not part of the wrapper's API stability surface.
package trace

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"time"
)

// Event is a single observation emitted by the wrapper.
type Event struct {
	At     time.Time      `json:"at"`
	Kind   string         `json:"kind"`
	Fields map[string]any `json:"fields,omitempty"`
}

// Emitter receives events. Implementations must be safe for concurrent use.
type Emitter interface {
	Emit(Event)
}

// Discard is an Emitter that drops every event it receives.
var Discard Emitter = discardEmitter{}

type discardEmitter struct{}

func (discardEmitter) Emit(Event) {}

// NewWriterEmitter returns an Emitter that writes one JSON-encoded event
// per line to w. Encoding errors are silently dropped — trace failures
// must not affect wrapper correctness.
func NewWriterEmitter(w io.Writer) Emitter {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &writerEmitter{encoder: enc}
}

type writerEmitter struct {
	mu      sync.Mutex
	encoder *json.Encoder
}

func (w *writerEmitter) Emit(e Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.encoder.Encode(e)
}

// NewSlogAdapter returns an Emitter that forwards events to logger as
// structured log records. The event's At becomes the record time; the
// Kind becomes the message; Fields become record attributes.
func NewSlogAdapter(logger *slog.Logger) Emitter {
	return &slogAdapter{logger: logger}
}

type slogAdapter struct {
	logger *slog.Logger
}

func (s *slogAdapter) Emit(e Event) {
	record := slog.NewRecord(e.At, slog.LevelInfo, e.Kind, 0)
	for k, v := range e.Fields {
		record.AddAttrs(slog.Any(k, v))
	}
	_ = s.logger.Handler().Handle(context.Background(), record)
}
