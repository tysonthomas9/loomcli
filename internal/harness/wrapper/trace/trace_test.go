package trace_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/harness/wrapper/trace"
)

func TestDiscardDropsEvents(t *testing.T) {
	trace.Discard.Emit(trace.Event{Kind: "anything"})
}

func TestWriterEmitterEncodesEvent(t *testing.T) {
	var buf bytes.Buffer
	emitter := trace.NewWriterEmitter(&buf)
	at := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	emitter.Emit(trace.Event{
		At:     at,
		Kind:   "pty_opened",
		Fields: map[string]any{"pid": 1234, "cols": 80},
	})

	line := buf.String()
	if !strings.HasSuffix(line, "\n") {
		t.Fatalf("expected newline-terminated JSON, got %q", line)
	}

	var got trace.Event
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nline: %q", err, line)
	}
	if got.Kind != "pty_opened" {
		t.Errorf("Kind = %q, want pty_opened", got.Kind)
	}
	if !got.At.Equal(at) {
		t.Errorf("At = %v, want %v", got.At, at)
	}
	if got.Fields["pid"].(float64) != 1234 {
		t.Errorf("Fields[pid] = %v, want 1234", got.Fields["pid"])
	}
}

func TestWriterEmitterOmitsNilFields(t *testing.T) {
	var buf bytes.Buffer
	emitter := trace.NewWriterEmitter(&buf)
	emitter.Emit(trace.Event{Kind: "wrapper_started"})

	line := buf.String()
	if strings.Contains(line, "fields") {
		t.Errorf("expected no fields key for nil Fields, got %q", line)
	}
}

func TestWriterEmitterIsConcurrentSafe(t *testing.T) {
	var buf bytes.Buffer
	emitter := trace.NewWriterEmitter(&buf)

	const goroutines = 50
	const eventsPerGoroutine = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := range goroutines {
		go func() {
			defer wg.Done()
			for i := range eventsPerGoroutine {
				emitter.Emit(trace.Event{
					Kind:   "test",
					Fields: map[string]any{"g": g, "i": i},
				})
			}
		}()
	}
	wg.Wait()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	want := goroutines * eventsPerGoroutine
	if len(lines) != want {
		t.Fatalf("got %d lines, want %d", len(lines), want)
	}
	for i, line := range lines {
		var ev trace.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("line %d invalid JSON: %v\n%q", i, err, line)
		}
	}
}

type capturingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler      { return h }

func TestSlogAdapterPreservesEventTimeAndFields(t *testing.T) {
	handler := &capturingHandler{}
	emitter := trace.NewSlogAdapter(slog.New(handler))

	at := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	emitter.Emit(trace.Event{
		At:     at,
		Kind:   "harness_exited",
		Fields: map[string]any{"exit_code": 0, "signal": ""},
	})

	if len(handler.records) != 1 {
		t.Fatalf("got %d records, want 1", len(handler.records))
	}
	r := handler.records[0]
	if r.Message != "harness_exited" {
		t.Errorf("Message = %q, want harness_exited", r.Message)
	}
	if !r.Time.Equal(at) {
		t.Errorf("Time = %v, want %v", r.Time, at)
	}
	gotAttrs := map[string]any{}
	r.Attrs(func(a slog.Attr) bool {
		gotAttrs[a.Key] = a.Value.Any()
		return true
	})
	if gotAttrs["exit_code"] != int64(0) {
		t.Errorf("exit_code = %v (%T), want int64(0)", gotAttrs["exit_code"], gotAttrs["exit_code"])
	}
}
