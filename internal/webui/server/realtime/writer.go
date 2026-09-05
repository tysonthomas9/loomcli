package realtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const frameWriteTimeout = 2 * time.Second

// Writer centralizes SSE wire-format concerns. It is not concurrency-safe.
type Writer struct {
	w          io.Writer
	controller responseController
	now        func() time.Time
}

type responseController interface {
	Flush() error
	SetWriteDeadline(time.Time) error
}

// NewWriter creates a new SSE writer, checking that the ResponseWriter supports Flusher.
func NewWriter(w http.ResponseWriter) (*Writer, error) {
	if _, ok := w.(http.Flusher); !ok {
		return nil, fmt.Errorf("ResponseWriter does not implement http.Flusher")
	}
	return newWriter(w, http.NewResponseController(w))
}

func newWriter(w io.Writer, controller responseController) (*Writer, error) {
	if w == nil {
		return nil, fmt.Errorf("SSE writer must not be nil")
	}
	if controller == nil {
		return nil, fmt.Errorf("SSE response controller must not be nil")
	}
	return &Writer{w: w, controller: controller, now: time.Now}, nil
}

// WriteRetry writes the retry interval to the SSE stream.
func (sw *Writer) WriteRetry(ms int) error {
	return sw.writeFrame(nil, nil, nil, &ms, nil)
}

// WriteEvent writes a named event with data to the SSE stream.
func (sw *Writer) WriteEvent(id int64, event, data string) error {
	return sw.WriteEventID(strconv.FormatInt(id, 10), event, data)
}

// WriteEventID writes a named event with a string event ID. SSE event IDs are
// opaque strings, which lets fleet-db cursors round-trip through Last-Event-ID.
func (sw *Writer) WriteEventID(id, event, data string) error {
	return sw.writeFrame(&id, &event, &data, nil, nil)
}

// WriteEventNoID writes a named event that does not advance Last-Event-ID.
func (sw *Writer) WriteEventNoID(event, data string) error {
	return sw.writeFrame(nil, &event, &data, nil, nil)
}

// WriteResync writes a resumable transport-repair frame. Its ID advances the
// connection cursor past a range that must be repaired from authoritative
// snapshots rather than delivered as mutations.
func (sw *Writer) WriteResync(id, reason string) error {
	data, err := json.Marshal(struct {
		Reason string `json:"reason"`
	}{Reason: reason})
	if err != nil {
		return err
	}
	return sw.WriteEventID(id, "resync", string(data))
}

// WriteComment writes a comment to the SSE stream.
func (sw *Writer) WriteComment(text string) error {
	return sw.writeFrame(nil, nil, nil, nil, &text)
}

func validateFrame(id, event *string, retry *int) error {
	if id != nil {
		if strings.ContainsAny(*id, "\r\n") {
			return fmt.Errorf("SSE event ID must not contain a carriage return or newline")
		}
		if strings.ContainsRune(*id, '\x00') {
			return fmt.Errorf("SSE event ID must not contain NUL")
		}
	}
	if event != nil && strings.ContainsAny(*event, "\r\n") {
		return fmt.Errorf("SSE event name must not contain a carriage return or newline")
	}
	if retry != nil && *retry < 0 {
		return fmt.Errorf("SSE retry must not be negative")
	}
	return nil
}

func buildFrame(id, event, data *string, retry *int, comment *string) string {
	var frame strings.Builder
	writeMultiline := func(prefix, text string) {
		text = strings.ReplaceAll(text, "\r\n", "\n")
		text = strings.ReplaceAll(text, "\r", "\n")
		for _, line := range strings.Split(text, "\n") {
			fmt.Fprintf(&frame, "%s%s\n", prefix, line)
		}
	}

	switch {
	case retry != nil:
		fmt.Fprintf(&frame, "retry: %d\n", *retry)
	case comment != nil:
		writeMultiline(": ", *comment)
	default:
		if id != nil {
			fmt.Fprintf(&frame, "id: %s\n", *id)
		}
		fmt.Fprintf(&frame, "event: %s\n", *event)
		writeMultiline("data: ", *data)
	}
	frame.WriteByte('\n')
	return frame.String()
}

func (sw *Writer) writeFrame(id, event, data *string, retry *int, comment *string) error {
	if err := validateFrame(id, event, retry); err != nil {
		return err
	}
	frame := buildFrame(id, event, data, retry, comment)
	deadlineErr := sw.controller.SetWriteDeadline(sw.now().Add(frameWriteTimeout))
	if deadlineErr != nil && !errors.Is(deadlineErr, http.ErrNotSupported) {
		return deadlineErr
	}

	_, writeErr := sw.w.Write([]byte(frame))
	var flushErr error
	if writeErr == nil {
		flushErr = sw.controller.Flush()
	}
	clearErr := sw.controller.SetWriteDeadline(time.Time{})
	if errors.Is(clearErr, http.ErrNotSupported) {
		clearErr = nil
	}
	if writeErr != nil {
		return writeErr
	}
	if flushErr != nil {
		return flushErr
	}
	return clearErr
}
