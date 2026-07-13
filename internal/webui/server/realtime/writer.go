package realtime

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// Writer centralizes SSE wire-format concerns.
type Writer struct {
	W       http.ResponseWriter
	Flusher http.Flusher
}

// NewWriter creates a new SSE writer, checking that the ResponseWriter supports Flusher.
func NewWriter(w http.ResponseWriter) (*Writer, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("ResponseWriter does not implement http.Flusher")
	}
	return &Writer{W: w, Flusher: flusher}, nil
}

// WriteRetry writes the retry interval to the SSE stream.
func (sw *Writer) WriteRetry(ms int) error {
	_, err := fmt.Fprintf(sw.W, "retry: %d\n\n", ms)
	sw.Flusher.Flush()
	return err
}

// WriteEvent writes a named event with data to the SSE stream.
func (sw *Writer) WriteEvent(id int64, event, data string) error {
	return sw.WriteEventID(strconv.FormatInt(id, 10), event, data)
}

// WriteEventID writes a named event with a string event ID. SSE event IDs are
// opaque strings, which lets fleet-db cursors round-trip through Last-Event-ID.
func (sw *Writer) WriteEventID(id, event, data string) error {
	_, err := io.WriteString(sw.W, fmt.Sprintf("id: %s\nevent: %s\ndata: %s\n\n", id, event, data))
	sw.Flusher.Flush()
	return err
}

// WriteEventNoID writes a named event without an id: field. It is used for
// control frames and intentionally non-resumable streams.
func (sw *Writer) WriteEventNoID(event, data string) error {
	_, err := io.WriteString(sw.W, fmt.Sprintf("event: %s\ndata: %s\n\n", event, data))
	sw.Flusher.Flush()
	return err
}

// WriteComment writes a comment to the SSE stream.
func (sw *Writer) WriteComment(text string) error {
	_, err := fmt.Fprintf(sw.W, ": %s\n\n", text)
	sw.Flusher.Flush()
	return err
}
